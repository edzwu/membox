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

// polishOutlineHeading is one outline item the Miru extractor found: either
// a real ATX heading (level 1–6) or a pseudo-heading candidate — an isolated
// short body line that looks like a heading lost in conversion (level 0,
// candidate true). Each carries a short sample of the following prose so the
// model can judge semantics without seeing the document; empty marks a
// section with no body at all (structural divider signal).
type polishOutlineHeading struct {
	Level     int    `json:"level"`
	Text      string `json:"text"`
	Sample    string `json:"sample,omitempty"`
	Candidate bool   `json:"candidate,omitempty"`
	Empty     bool   `json:"empty,omitempty"`
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
		if h.Candidate {
			if h.Level != 0 {
				return fmt.Errorf("candidate heading %d must have level 0", i)
			}
		} else if h.Level < 1 || h.Level > 6 {
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
	b.WriteString(`你是文档结构编辑。下面是一篇 Markdown 文档的大纲：按顺序列出每一项的序号、当前级别、文本，以及紧随其后的一小段正文采样（供你判断语义）。大纲中有两类条目：
- 标题（H1–H6）：原文里已是 Markdown 标题；
- 候选标题：原文里是普通正文行，但疑似转换（如 PDF 提取）丢失的标题。
任务：给出每一项应有的层级与文本，规则：
1. 若全文只有一个 H1，它是文档主标题，保持 H1；正文章节从 H2 开始，子节依次 H3、H4，不要跳级。
2. 按语义判断归属：内容上是上一节子主题的标题降一级；并列主题的标题同级。
3. 标注「分隔大节」的标题（本节无正文、无编号）：紧随其后的小节是它的内容，它必须比后面的小节高一级——若当前同级或更低，把它升上去（例如它 H3、后面 H3，则它升为 H2、后面保持 H3）。它开启一个新部分，不是前一节的内容（本条优先于规则 2）。
4. 标注「编号容器」的标题（本节无正文、带 Part / Chapter / Appendix / 第 X 章 编号）：保持原级，紧跟其后的内容是它的子节、应比它低一级，降级级联（H2→H3、H3→H4）。
5. 候选标题：根据它自身文本与后文采样判断。全部大写（如 "SOMEWHAT HAPPY"）、或与上下文标题风格一致的短语，几乎确定是转换丢失的标题——给出应有层级（参照上下文同级标题），文本逐字保留；多个候选标题连续堆叠时更是如此（正文不会连排多个孤立短行），应全部提升、不要只提升其中一个；明显是句子（有主谓结构、是强调或过渡句）才返回 level 0 保持正文。
6. 去掉标题里的手工序号（如 "1." "3)"），阅读器会自动编号，保留会显示成重复编号；但 "Part I" "Chapter 2" "第3章" 这类构成标题本身的编号词保留。
7. 标注「疑似乱码」的标题是粘贴错误（文件名、URL 残片、slug），必须根据该节正文采样改写成一个简洁明了的标题（例如采样讲 Factory Vfs，标题就写 "Factory Vfs"）。其他标题逐字保留原文——包括大小写与标点，不要改写、翻译或规范化。
8. 以上规则都不适用且拿不准时，保持原级别与原文，宁少勿改。不要增删条目，不要编造内容。
输出：严格的 JSON 数组，第 i 个元素对应第 i 项，格式 {"level": 2, "text": "标题"}；候选标题保持正文时用 {"level": 0, "text": "原文"}。只输出 JSON，不要解释。

示例输入：
0. H1 书摘
   采样：本文节选自……
1. 候选标题（正文行）「背景」
   采样：1949 年，作者出生于江南小镇……
2. H2 早年经历
   采样：他从小好学……
3. H3 转折（本节无正文——分隔大节）
4. H3 第一次创业
   采样：2001 年，他来到深圳……
5. 候选标题（正文行）「他感到前所未有的疲惫」
   采样：那段日子他常常失眠……
示例输出：
[{"level":1,"text":"书摘"},{"level":2,"text":"背景"},{"level":2,"text":"早年经历"},{"level":2,"text":"转折"},{"level":3,"text":"第一次创业"},{"level":0,"text":"他感到前所未有的疲惫"}]
（要点：候选「背景」提升为与上下文同级的 H2；分隔大节「转折」从 H3 升为 H2，其后的「第一次创业」保持 H3 作为它的内容；「他感到前所未有的疲惫」是句子不是标题，保持 level 0。）

标题大纲：
`)
	for i, h := range headings {
		if h.Candidate {
			fmt.Fprintf(&b, "%d. 候选标题（正文行）「%s」", i, h.Text)
		} else {
			fmt.Fprintf(&b, "%d. H%d %s", i, h.Level, h.Text)
			if looksGarbledHeading(h.Text) {
				b.WriteString("（疑似乱码/粘贴错误）")
			}
			if h.Empty || strings.TrimSpace(h.Sample) == "" {
				if looksNumberedContainer(h.Text) {
					b.WriteString("（本节无正文——编号容器）")
				} else {
					b.WriteString("（本节无正文——分隔大节）")
				}
			}
		}
		b.WriteString("\n")
		if sample := strings.TrimSpace(h.Sample); withSamples && sample != "" {
			fmt.Fprintf(&b, "   采样：%s\n", sample)
		}
	}
	return b.String()
}

var numberedContainerRe = regexp.MustCompile(`(?i)^(part|partie|chapter|chapitre|appendix|book|volume|section|lesson|lecture)\s+([0-9]+|[ivxlcdm]+|[A-Z])\b|^第[0-9一二三四五六七八九十百千]+[章部篇卷回节]`)

// looksNumberedContainer detects structural grouping titles ("Part I",
// "Chapter 2", "第3章") — code does this mechanical classification so the
// prompt can point the model at the right empty-section rule directly.
func looksNumberedContainer(text string) bool {
	return numberedContainerRe.MatchString(strings.TrimSpace(text))
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
// each [...] candidate, then enforces the contract: one edit per outline
// item; real headings keep level 1..6, candidates take 0 (left as body —
// text forced back to the original) or 1..6 (promoted); promoted or kept
// headings need non-empty text within the length cap.
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
			if headings[i].Candidate && edit.Level == 0 {
				edits[i] = polishOutlineEdit{Level: 0, Text: headings[i].Text}
				continue
			}
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
