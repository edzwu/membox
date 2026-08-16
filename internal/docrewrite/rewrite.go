// Package docrewrite turns low-quality Markdown (typically PDF conversion
// debris) into readable notes via LLM completion.
//
// Default model path is local qwen3:14b through mmd (same stack as PDF
// structure planning / translation). Cloud deepseek-v4-flash is opt-in.
//
// Large documents are rewritten in heading-aligned chunks so each model call
// stays within local context / mmd prompt limits — the full file is never
// stuffed into one prompt.
package docrewrite

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"membox/internal/assist"
	"membox/internal/translation"
)

const (
	LocalProvider = translation.DefaultProvider // "ollama" (label)
	LocalModel    = translation.DefaultModel    // "qwen3:14b"

	DeepseekProvider = assist.DefaultProvider // "deepseek"
	DeepseekModel    = assist.DefaultModel    // "deepseek-v4-flash"
)

// ModelSpec is a resolved provider/model pair.
type ModelSpec struct {
	Provider string
	Model    string
	// Local means mmd /v1/llm/complete (qwen3:14b). False → Pi RPC (cloud).
	Local bool
	Label string
}

// ParseModel resolves CLI --model values.
//
//	"" | "local" | "qwen" | "qwen3:14b"  → local qwen3:14b via mmd
//	"deepseek" | "deepseek-flash" |
//	"deepseek/deepseek-v4-flash"         → deepseek-v4-flash via Pi
//	"provider/model"                     → Pi (or local if provider is ollama)
func ParseModel(raw string) (ModelSpec, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", "local", "qwen", "qwen3", "qwen3:14b", "ollama", "ollama/qwen3:14b":
		return ModelSpec{
			Provider: LocalProvider, Model: LocalModel, Local: true,
			Label: "local " + LocalModel,
		}, nil
	case "deepseek", "deepseek-flash", "flash", "deepseek/deepseek-v4-flash":
		return ModelSpec{
			Provider: DeepseekProvider, Model: DeepseekModel, Local: false,
			Label: DeepseekProvider + "/" + DeepseekModel,
		}, nil
	}
	if i := strings.Index(s, "/"); i > 0 {
		provider, model := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
		if provider == "" || model == "" {
			return ModelSpec{}, fmt.Errorf("invalid model %q (want provider/model)", raw)
		}
		local := provider == "ollama" || provider == "membox-ollama"
		label := provider + "/" + model
		if local {
			label = "local " + model
		}
		return ModelSpec{Provider: provider, Model: model, Local: local, Label: label}, nil
	}
	return ModelSpec{}, fmt.Errorf("unknown model %q (try local, qwen3:14b, deepseek, or provider/model)", raw)
}

// Request is one rewrite job.
type Request struct {
	Title    string
	Markdown string
	Model    ModelSpec
	// Hint is optional extra user instruction (e.g. "C/C++ fences must be cpp").
	// Injected into every chunk (and lightly into seam) prompt.
	Hint string
}

// Result is the rewritten Markdown body.
type Result struct {
	Markdown   string
	Provider   string
	Model      string
	Label      string
	Chunks     int
	ChunkRunes int // budget used per chunk
}

// Progress reports multi-chunk rewrite status.
type Progress struct {
	Done   int
	Total  int
	Detail string
}

// Runner performs rewrite completion (chunked when needed).
type Runner struct {
	Home       string // membox home (mmd socket) + Pi cwd
	PiPath     string // optional absolute pi
	OnProgress func(Progress)
}

// Rewrite runs the model on heading-aligned chunks and joins the result.
func (r Runner) Rewrite(ctx context.Context, req Request) (Result, error) {
	src := strings.TrimSpace(req.Markdown)
	if src == "" {
		return Result{}, fmt.Errorf("document body is empty")
	}
	spec := req.Model
	if spec.Model == "" {
		var err error
		spec, err = ParseModel("")
		if err != nil {
			return Result{}, err
		}
	}
	budget := chunkBudget(spec.Local)
	plan := splitForRewrite(src, budget)
	total := len(plan.Parts)
	if total == 0 {
		// front matter only
		out := strings.TrimSpace(plan.FrontMatter)
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return Result{Markdown: out, Provider: spec.Provider, Model: spec.Model, Label: spec.Label, Chunks: 0, ChunkRunes: budget}, nil
	}

	prevTailBudget, seamSide := continuityBudget(spec.Local)
	rewritten := make([]string, 0, total)
	var prevTail string
	for i, part := range plan.Parts {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if r.OnProgress != nil {
			r.OnProgress(Progress{
				Done: i, Total: total,
				Detail: fmt.Sprintf("chunk %d/%d (%d runes)", i+1, total, utf8.RuneCountInString(part)),
			})
		}
		prompt := BuildChunkPrompt(req.Title, part, i+1, total, prevTail, req.Hint)
		text, err := r.complete(ctx, spec, prompt)
		if err != nil {
			return Result{}, fmt.Errorf("chunk %d/%d: %w", i+1, total, err)
		}
		out := stripWrapperFences(strings.TrimSpace(text))
		if out == "" {
			return Result{}, fmt.Errorf("chunk %d/%d: model returned empty rewrite from %s", i+1, total, spec.Label)
		}
		rewritten = append(rewritten, out)
		prevTail = tailRunes(out, prevTailBudget)
	}
	// Second pass: polish each join so cuts between chunks read naturally.
	if total > 1 {
		seams := total - 1
		for i := 0; i < seams; i++ {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if r.OnProgress != nil {
				r.OnProgress(Progress{
					Done: total + i, Total: total + seams,
					Detail: fmt.Sprintf("seam %d/%d", i+1, seams),
				})
			}
			leftTail := tailRunes(rewritten[i], seamSide)
			rightHead := headRunes(rewritten[i+1], seamSide)
			if leftTail == "" || rightHead == "" {
				continue
			}
			prompt := BuildSeamPrompt(req.Title, leftTail, rightHead, i+1, seams, req.Hint)
			text, err := r.complete(ctx, spec, prompt)
			if err != nil {
				// Seam polish is best-effort; keep the hard join.
				continue
			}
			polished := stripWrapperFences(strings.TrimSpace(text))
			nl, nr, ok := applySeamReplacement(rewritten[i], rewritten[i+1], leftTail, rightHead, polished)
			if ok {
				rewritten[i], rewritten[i+1] = nl, nr
			}
		}
		if r.OnProgress != nil {
			r.OnProgress(Progress{Done: total + seams, Total: total + seams, Detail: "joining chunks"})
		}
	} else if r.OnProgress != nil {
		r.OnProgress(Progress{Done: total, Total: total, Detail: "joining chunks"})
	}
	joined := NormalizeCodeFences(joinRewriteParts(plan.FrontMatter, rewritten))
	if strings.TrimSpace(joined) == "" {
		return Result{}, fmt.Errorf("model returned empty rewrite from %s", spec.Label)
	}
	return Result{
		Markdown:   joined,
		Provider:   spec.Provider,
		Model:      spec.Model,
		Label:      spec.Label,
		Chunks:     total,
		ChunkRunes: budget,
	}, nil
}

func (r Runner) complete(ctx context.Context, spec ModelSpec, prompt string) (string, error) {
	if spec.Local {
		return r.completeLocal(ctx, prompt)
	}
	return r.completeCloud(ctx, spec.Provider, spec.Model, prompt)
}

func (r Runner) completeLocal(ctx context.Context, prompt string) (string, error) {
	home := strings.TrimSpace(r.Home)
	client := translation.MMDClient{
		SocketPath: translation.SocketPath(home),
		Ensure: func(ctx context.Context) error {
			return translation.EnsureMMD(ctx, home)
		},
	}
	text, err := client.Complete(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("local model (%s) via mmd: %w", LocalModel, err)
	}
	return text, nil
}

func (r Runner) completeCloud(ctx context.Context, provider, model, prompt string) (string, error) {
	home := strings.TrimSpace(r.Home)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	piPath := resolvePiPath(r.PiPath, home)
	instruction, selection := splitPrompt(prompt)
	runner := assist.Runner{PiPath: piPath, Home: home, Provider: provider, Model: model}
	var collected strings.Builder
	err := runner.Stream(ctx, assist.Request{
		Mode:        assist.ModeEdit,
		Instruction: instruction,
		Selection:   selection,
	}, func(ev assist.Event) error {
		switch ev.Type {
		case "delta":
			if ev.Text != "" {
				collected.WriteString(ev.Text)
			}
		case "done":
			if strings.TrimSpace(ev.Replacement) != "" {
				collected.Reset()
				collected.WriteString(ev.Replacement)
			} else if ev.Text != "" && collected.Len() == 0 {
				collected.WriteString(ev.Text)
			}
		case "error":
			if ev.Error != "" {
				return fmt.Errorf("%s", ev.Error)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("pi %s/%s: %w", provider, model, err)
	}
	return collected.String(), nil
}

func resolvePiPath(configured, home string) string {
	piPath := strings.TrimSpace(configured)
	if piPath == "" {
		piPath = "pi"
	}
	if filepath.IsAbs(piPath) {
		return piPath
	}
	if resolved, err := exec.LookPath(piPath); err == nil {
		return resolved
	}
	for _, candidate := range []string{
		filepath.Join(home, ".local/bin/pi"),
		"/opt/homebrew/bin/pi",
		"/usr/local/bin/pi",
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return piPath
}

func splitPrompt(full string) (instruction, selection string) {
	const marker = "---原文开始---\n"
	const endMarker = "\n---原文结束---"
	i := strings.Index(full, marker)
	if i < 0 {
		return "Rewrite the selected Markdown for readability. Output ONLY the rewritten Markdown for this part.", strings.TrimSpace(full)
	}
	instruction = strings.TrimSpace(full[:i])
	rest := full[i+len(marker):]
	if j := strings.Index(rest, endMarker); j >= 0 {
		selection = rest[:j]
	} else {
		selection = rest
	}
	selection = strings.TrimSpace(selection)
	if selection == "" {
		selection = "."
	}
	if instruction == "" {
		instruction = "Rewrite the selected Markdown for readability."
	}
	instruction += "\n\nOutput ONLY the rewritten Markdown for this part (no prose before/after, no wrapping ```markdown fence)."
	return instruction, selection
}

// BuildChunkPrompt is the per-chunk rewrite brief.
// prevTail is optional already-rewritten ending of the previous chunk (context only).
// hint is optional extra user instruction injected after the default rules.
func BuildChunkPrompt(title, markdown string, index, total int, prevTail, hint string) string {
	title = strings.TrimSpace(title)
	head := "你是技术文档编辑。下面是可读性较差的 Markdown 片段（常见于 PDF 转换：碎行、乱码代码、错误标题层级、公式损坏）。"
	if title != "" {
		head += "\n文档标题提示：" + title
	}
	if total > 1 {
		head += fmt.Sprintf("\n这是长文档的第 %d/%d 段。只改写本段；不要输出其他段的内容；不要杜撰本段之外的章节。", index, total)
	}
	prev := strings.TrimSpace(prevTail)
	prevBlock := ""
	if prev != "" && total > 1 && index > 1 {
		prevBlock = "\n\n【上文已改写结尾——仅供衔接语气/术语，不要重复输出这段】\n" + prev + "\n"
	}
	hintBlock := formatHintBlock(hint)
	return head + prevBlock + `

请输出可直接拼接进全文的 Markdown，要求：
1. 保留原意与技术细节；不要编造原文没有的 API/库名/数据。看不懂的乱码可据上下文合理还原，或标注「（原文排版损坏）」。
2. 合理小标题（H2/H3）；若本段已有标题则保留并理顺，不要强行再套一层文档总标题。
3. 公式用 $...$ 或 $$...$$。
4. 代码必须用 fenced code block，并正确标明语言：
   - C/C++ 源码一律用 cpp（禁止 txt / text / plain / c）
   - shell 用 bash；JSON 用 json；Python 用 python；纯日志/输出才用 text
5. 删除无意义重复装饰图；有信息量的图片 URL 可保留。
6. 保留系列返回链接（若本段含有）。
7. 开头承接上文语气，但不要复述上文已写内容。
8. 不要输出解释，不要用 markdown 代码围栏包住全文——只输出本段 Markdown。
` + hintBlock + `
---原文开始---
` + strings.TrimSpace(markdown) + `
---原文结束---
`
}

// BuildSeamPrompt asks the model to smooth only the join between two chunks.
func BuildSeamPrompt(title, leftTail, rightHead string, index, total int, hint string) string {
	title = strings.TrimSpace(title)
	head := "你是技术文档编辑。下面两段文字是同一文档相邻两块改写结果在拼接处的窗口。"
	if title != "" {
		head += "\n文档标题提示：" + title
	}
	head += fmt.Sprintf("\n这是第 %d/%d 个接缝。", index, total)
	hintBlock := formatHintBlock(hint)
	return head + `

任务：只微调衔接，使跨段阅读自然。
- 保留几乎全部信息与术语，不要大幅扩写或删节。
- 可修：断句、重复标题、指代突然断裂、列表编号不连续。
- 不要加入原文没有的新章节/API。
- 输出一段完整 Markdown，用于替换「左窗 + 空行 + 右窗」；中间用空行分开左右两半更佳。
- 不要解释，不要代码围栏包全文。
` + hintBlock + `
---左窗（上一段结尾）---
` + strings.TrimSpace(leftTail) + `

---右窗（下一段开头）---
` + strings.TrimSpace(rightHead) + `
`
}

func formatHintBlock(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return ""
	}
	return "\n【用户额外要求——优先遵守】\n" + hint + "\n"
}

// BuildPrompt rewrites an entire small document in one shot (tests / helpers).
func BuildPrompt(title, markdown string) string {
	return BuildChunkPrompt(title, markdown, 1, 1, "", "")
}

// BuildPromptWithHint is BuildPrompt plus a user hint (tests / helpers).
func BuildPromptWithHint(title, markdown, hint string) string {
	return BuildChunkPrompt(title, markdown, 1, 1, "", hint)
}

func stripWrapperFences(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "```" {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
