package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"membox/internal/assist"
	"membox/internal/translation"
)

// Outline polish is one small cloud call (default deepseek via Pi RPC), but
// the "local" fallback shares mmd with every other local feature — serialize
// so a burst of clicks cannot queue inside the daemon.
var polishOutlineMu sync.Mutex

const (
	polishOutlineMaxHeadings  = 500
	polishOutlineMaxTextLen   = 300
	polishOutlineMaxSampleLen = 200
	// Samples included, keep the prompt comfortably under the assist
	// selection cap (60000 runes); headings-only may use the full budget.
	outlinePromptBudgetRunes = 45000
	outlinePromptMaxRunes    = 58000
)

// polishOutlineHeading is one heading the Miru outline extractor found,
// plus a short sample of the section's opening body so the model can judge
// semantics (and recognize garbled headings) without seeing the document.
type polishOutlineHeading struct {
	Level  int    `json:"level"`
	Text   string `json:"text"`
	Sample string `json:"sample,omitempty"`
}

// polishOutlineEdit is the model's correction for one heading.
type polishOutlineEdit struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// polishOutlineFunc is the model seam (Pi cloud / mmd local); tests inject
// a fake.
type polishOutlineFunc func(ctx context.Context, home, model, prompt string) (label string, text string, err error)

var runPolishOutline polishOutlineFunc = defaultPolishOutline

func defaultPolishOutline(ctx context.Context, home, model, prompt string) (string, string, error) {
	if model == "local" {
		client := translation.MMDClient{
			SocketPath: translation.SocketPath(home),
			Ensure: func(ctx context.Context) error {
				return translation.EnsureMMD(ctx, home)
			},
		}
		text, err := client.Complete(ctx, prompt)
		if err != nil {
			return "", "", fmt.Errorf("local model (%s) via mmd: %w", translation.DefaultModel, err)
		}
		return "local " + translation.DefaultModel, text, nil
	}
	runner := assist.Runner{Home: home}
	var collected strings.Builder
	err := runner.Stream(ctx, assist.Request{
		Mode:        assist.ModeAsk,
		Instruction: "按用户要求只输出 JSON 数组，不要输出任何解释。",
		Selection:   prompt,
	}, func(ev assist.Event) error {
		switch ev.Type {
		case "delta":
			collected.WriteString(ev.Text)
		case "done":
			if ev.Text != "" && collected.Len() == 0 {
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
		return "", "", fmt.Errorf("pi %s/%s: %w", assist.DefaultProvider, assist.DefaultModel, err)
	}
	return assist.DefaultProvider + "/" + assist.DefaultModel, collected.String(), nil
}

// handlePolishOutline maps a document's heading outline to corrected levels.
// The frontend extracts headings (fence-aware, lines only — the body never
// leaves the browser), the model judges hierarchy, and the frontend applies
// the levels deterministically. Cheap enough for deepseek by default.
func (s *Server) handlePolishOutline(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	if s.home == "" {
		http.Error(writer, "membox home is not configured", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Headings []polishOutlineHeading `json:"headings"`
		Model    string                 `json:"model"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(writer, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := validateOutlineHeadings(payload.Headings); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if !polishOutlineMu.TryLock() {
		http.Error(writer, "another outline polish is running", http.StatusConflict)
		return
	}
	defer polishOutlineMu.Unlock()

	prompt := buildOutlinePrompt(payload.Headings, true)
	if len([]rune(prompt)) > outlinePromptBudgetRunes {
		// Over budget: drop the section samples first — levels alone still
		// produce a good hierarchy, samples only drive garbled-heading fixes.
		prompt = buildOutlinePrompt(payload.Headings, false)
	}
	if len([]rune(prompt)) > outlinePromptMaxRunes {
		http.Error(writer, fmt.Sprintf("outline too large even without samples (%d headings)", len(payload.Headings)), http.StatusBadRequest)
		return
	}
	label, text, err := runPolishOutline(request.Context(), s.home, strings.TrimSpace(payload.Model), prompt)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	edits, err := parseOutlineEdits(text, payload.Headings)
	if err != nil {
		http.Error(writer, fmt.Sprintf("model returned unusable edits: %s", err), http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"edits": edits,
		"label": label,
	})
}

func validateOutlineHeadings(headings []polishOutlineHeading) error {
	if len(headings) == 0 {
		return fmt.Errorf("headings are required")
	}
	if len(headings) > polishOutlineMaxHeadings {
		return fmt.Errorf("too many headings (%d > %d)", len(headings), polishOutlineMaxHeadings)
	}
	for i, h := range headings {
		if h.Level < 1 || h.Level > 6 {
			return fmt.Errorf("heading %d has invalid level %d", i, h.Level)
		}
		if len([]rune(h.Text)) > polishOutlineMaxTextLen {
			return fmt.Errorf("heading %d text too long", i)
		}
		if len([]rune(h.Sample)) > polishOutlineMaxSampleLen {
			return fmt.Errorf("heading %d sample too long", i)
		}
	}
	return nil
}

func buildOutlinePrompt(headings []polishOutlineHeading, withSamples bool) string {
	var b strings.Builder
	b.WriteString(`你是文档结构编辑。下面是一篇 Markdown 文档的标题大纲：每个标题有序号、当前级别、标题文本，以及该节开头的一小段正文采样（供你判断语义）。
任务：给出每个标题应有的层级与文本，规则：
1. 若全文只有一个 H1，它是文档主标题，保持 H1；正文章节从 H2 开始，子节依次 H3、H4，不要跳级。
2. 按语义判断归属：内容上是上一节子主题的标题降一级；并列主题的标题同级。
3. 容器标题：标注了「本节无正文」的 Part / Chapter / Appendix / 第 X 章 等带编号的结构标题是分组容器——紧跟其后的 Chapter/小节是它的内容，应比它低一级（例如 Part 保持 H2，其下的 Chapter 从 H2 降为 H3）；降级是级联的，容器内容原有的子节随之顺降（H3→H4）。容器本身不要降到与内容同级。
4. 去掉标题里的手工序号（如 "1." "3)"），阅读器会自动编号，保留会显示成重复编号；但 "Part I" "Chapter 2" "第3章" 这类构成标题本身的编号词保留。
5. 标记者「疑似乱码」的标题是粘贴错误（文件名、URL 残片、slug），必须根据该节正文采样改写成一个简洁明了的标题（例如采样讲 Factory Vfs，标题就写 "Factory Vfs"）。其他标题逐字保留原文。
6. 拿不准就保持原级别与原文，宁少勿改。不要增删标题，不要编造内容。
输出：严格的 JSON 数组，第 i 个元素对应第 i 个标题，格式 {"level": 2, "text": "标题"}。只输出 JSON，不要解释。

标题大纲：
`)
	for i, h := range headings {
		fmt.Fprintf(&b, "%d. H%d %s", i, h.Level, h.Text)
		if looksGarbledHeading(h.Text) {
			b.WriteString("（疑似乱码/粘贴错误）")
		}
		if strings.TrimSpace(h.Sample) == "" {
			b.WriteString("（本节无正文）")
		}
		b.WriteString("\n")
		if sample := strings.TrimSpace(h.Sample); withSamples && sample != "" {
			fmt.Fprintf(&b, "   采样：%s\n", sample)
		}
	}
	return b.String()
}

// looksGarbledHeading detects paste artifacts: filename/URL-slug headings like
// "oss-filesystem-exploration" or "readme.md" — a single lowercase ASCII
// slug token with a separator. Real titles contain spaces or non-ASCII text.
func looksGarbledHeading(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, " \t") {
		return false
	}
	if !strings.ContainsAny(text, "-._") {
		return false
	}
	for _, r := range text {
		if r > 127 {
			return false
		}
	}
	return true
}

var outlineJSONRe = regexp.MustCompile(`\[[\s\S]*?\]`)

// parseOutlineEdits tolerates prose/fences around the JSON array by trying
// each [...] candidate, then enforces the contract: one edit per heading,
// level in 1..6, non-empty text within the length cap.
func parseOutlineEdits(text string, headings []polishOutlineHeading) ([]polishOutlineEdit, error) {
	var lastErr error
	for _, match := range outlineJSONRe.FindAllString(text, -1) {
		var edits []polishOutlineEdit
		if err := json.Unmarshal([]byte(match), &edits); err != nil {
			lastErr = err
			continue
		}
		if len(edits) != len(headings) {
			lastErr = fmt.Errorf("got %d edits for %d headings", len(edits), len(headings))
			continue
		}
		bad := ""
		for i, edit := range edits {
			if edit.Level < 1 || edit.Level > 6 {
				bad = fmt.Sprintf("level %d out of range at index %d", edit.Level, i)
				break
			}
			edit.Text = strings.TrimSpace(edit.Text)
			if edit.Text == "" {
				bad = fmt.Sprintf("empty text at index %d", i)
				break
			}
			if len([]rune(edit.Text)) > polishOutlineMaxTextLen {
				bad = fmt.Sprintf("text too long at index %d", i)
				break
			}
			edits[i] = edit
		}
		if bad != "" {
			lastErr = fmt.Errorf("%s", bad)
			continue
		}
		return edits, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no JSON array in response")
	}
	return nil, lastErr
}
