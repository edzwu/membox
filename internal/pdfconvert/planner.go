package pdfconvert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ChapterPlan is one LLM-proposed chapter boundary. The anchor must quote
// text that exists verbatim in the document; the planner never rewrites
// content, it only proposes where a reader-friendly chapter begins.
type ChapterPlan struct {
	Title  string
	Anchor string
}

// StructurePlanner proposes chapter boundaries from a compact structural
// sketch of a document that deterministic splitting could not handle.
type StructurePlanner interface {
	PlanChapters(ctx context.Context, sketch string) ([]ChapterPlan, error)
}

// CompletionFunc is one shot of a local LLM (supplied by the host, typically
// backed by mmd → Pi → qwen3:14b).
type CompletionFunc func(ctx context.Context, prompt string) (string, error)

// LLMStructurePlanner implements StructurePlanner through a CompletionFunc.
type LLMStructurePlanner struct {
	Complete CompletionFunc
}

func (p LLMStructurePlanner) PlanChapters(ctx context.Context, sketch string) ([]ChapterPlan, error) {
	if p.Complete == nil {
		return nil, fmt.Errorf("no LLM completion configured")
	}
	prompt := structurePrompt(sketch)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := p.Complete(ctx, prompt)
		if err != nil {
			return nil, err
		}
		plans, err := parseChapterPlans(raw)
		if err == nil {
			return plans, nil
		}
		lastErr = err
		prompt = structurePrompt(sketch) + "\n\n再次提醒：上一次输出不是合法的 JSON 或包含了外部内容。只输出一个 JSON 对象，anchor 必须逐字复制草图行的文本。"
	}
	return nil, lastErr
}

// structureSketchBudget must fit the local model's context together with the
// prompt and the JSON plan; 8KB stays safely inside num_ctx 8192.
const structureSketchBudget = 8 << 10

// BuildStructureSketch compresses a long document into a heading map plus
// paragraph openings, sampled to fit the local model's context. Line numbers
// give the model ordering cues; anchor verification later ignores them.
func BuildStructureSketch(markdown string) string {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	lineStarts := lineStartOffsets(source)
	lineOf := func(offset int) int {
		lo, hi := 0, len(lineStarts)
		for lo < hi {
			mid := (lo + hi) / 2
			if lineStarts[mid] <= offset {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo // 1-based line number
	}

	type entry struct {
		line   int
		marker string
		text   string
	}
	entries := make([]entry, 0, 512)
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		switch node.Kind() {
		case ast.KindHeading:
			heading := node.(*ast.Heading)
			title := strings.TrimSpace(string(heading.Text(source)))
			if title == "" {
				continue
			}
			offset := 0
			if heading.Lines().Len() > 0 {
				offset = heading.Lines().At(0).Start
			}
			entries = append(entries, entry{line: lineOf(offset), marker: fmt.Sprintf("H%d", heading.Level), text: title})
		case ast.KindParagraph:
			if node.Lines().Len() == 0 {
				continue
			}
			segment := node.Lines().At(0)
			first := strings.TrimSpace(string(segment.Value(source)))
			if len([]rune(first)) < 8 || strings.HasPrefix(first, "![]") || strings.HasPrefix(first, "|") {
				continue
			}
			runes := []rune(first)
			if len(runes) > 90 {
				first = string(runes[:90]) + "…"
			}
			entries = append(entries, entry{line: lineOf(segment.Start), marker: "P", text: first})
		}
	}

	build := func(stride int) string {
		var out strings.Builder
		last := len(entries) - 1
		for index, item := range entries {
			if item.marker == "P" && stride > 1 && index%stride != 0 && index != last {
				continue
			}
			fmt.Fprintf(&out, "%s @%d %s\n", item.marker, item.line, item.text)
		}
		return out.String()
	}
	sketch := build(1)
	for stride := 2; len(sketch) > structureSketchBudget && stride <= 16; stride++ {
		sketch = build(stride)
	}
	if len(sketch) > structureSketchBudget {
		sketch = sketch[:structureSketchBudget]
	}
	return sketch
}

func lineStartOffsets(source []byte) []int {
	starts := []int{0}
	for index, b := range source {
		if b == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func structurePrompt(sketch string) string {
	return `任务：从 Markdown 书籍的结构草图中选择章节边界。这是机械的文本定位任务，不是关于这本书的问答；禁止输出任何外部知识、链接或评论。

草图格式：每行是 "H<层级> @行号 标题" 或 "P @行号 段落开头"。段落可能按间隔抽样。

输出示例（仅演示 JSON 格式，不要复制示例中的文字）：
{"chapters":[{"title":"<章节标题>","anchor":"<草图中某行文本的开头>"},{"title":"<章节标题>","anchor":"<另一行文本的开头>"}]}

硬性规则：
1. anchor 必须逐字复制草图中某一行 @行号 后面的文本开头（不超过 60 个字符），不得包含 H/P 标记、行号或竖线表格符。
2. 2-30 个章节，按正文先后顺序排列。
3. 跳过封面、版权页和目录；以 | 开头的表格行是目录，不是正文；紧跟在 Contents/目录 标题之后的标题行也属于目录区，不要选为 anchor，正文从目录区之后开始。
4. 序言/引言/正文各部分/后记都应成为章节；title 输出可读标题。
5. 不确定时宁可选少的边界，也不要编造草图中不存在的文字。
6. 不要输出 ` + "```" + `、解释、链接或任何 JSON 以外的字符。

结构草图：
` + sketch
}

func parseChapterPlans(raw string) ([]ChapterPlan, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "```")
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
	if start := strings.IndexByte(raw, '{'); start >= 0 {
		if end := strings.LastIndexByte(raw, '}'); end >= start {
			raw = raw[start : end+1]
		}
	} else if start := strings.IndexByte(raw, '['); start >= 0 {
		if end := strings.LastIndexByte(raw, ']'); end >= start {
			raw = `{"chapters":` + raw[start:end+1] + `}`
		}
	}
	var payload struct {
		Chapters []struct {
			Title  string `json:"title"`
			Anchor string `json:"anchor"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode chapter plan: %w (model output: %.300s)", err, raw)
	}
	plans := make([]ChapterPlan, 0, len(payload.Chapters))
	for _, chapter := range payload.Chapters {
		title := sanitizePlanText(chapter.Title, 120)
		anchor := sanitizePlanText(chapter.Anchor, 90)
		if anchor == "" {
			continue
		}
		if title == "" {
			title = anchor
		}
		plans = append(plans, ChapterPlan{Title: title, Anchor: anchor})
	}
	if len(plans) < 2 {
		return nil, fmt.Errorf("plan has %d usable chapters, need at least 2", len(plans))
	}
	return plans, nil
}

func sanitizePlanText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, "# \t\"'`")
	runes := []rune(value)
	if len(runes) > maxRunes {
		value = string(runes[:maxRunes])
	}
	return strings.TrimSpace(value)
}
