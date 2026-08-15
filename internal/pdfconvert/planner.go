package pdfconvert

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ChapterPlan is one LLM-proposed chapter boundary. Line refers to a numbered
// line in the structural sketch; the model only picks lines, never rewrites
// content. When the book has a parseable TOC, Title comes from the TOC entry
// and Line maps a TOC entry to a real body position.
type ChapterPlan struct {
	Title string
	Line  int
}

// PlanRequest carries the structural sketch plus the book's real TOC entries
// (empty when the document has no parseable table of contents).
type PlanRequest struct {
	Entries []string
	Sketch  string
}

// StructurePlanner proposes chapter boundaries from a compact structural
// sketch of a document that deterministic splitting could not handle.
type StructurePlanner interface {
	PlanChapters(ctx context.Context, request PlanRequest) ([]ChapterPlan, error)
}

// CompletionFunc is one shot of a local LLM (supplied by the host, typically
// backed by mmd → Pi → qwen3:14b).
type CompletionFunc func(ctx context.Context, prompt string) (string, error)

// LLMStructurePlanner implements StructurePlanner through a CompletionFunc.
type LLMStructurePlanner struct {
	Complete CompletionFunc
}

func (p LLMStructurePlanner) PlanChapters(ctx context.Context, request PlanRequest) ([]ChapterPlan, error) {
	if p.Complete == nil {
		return nil, fmt.Errorf("no LLM completion configured")
	}
	prompt := structurePrompt(request)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := p.Complete(ctx, prompt)
		if err != nil {
			return nil, err
		}
		plans, err := parseChapterPlans(raw, request.Entries)
		if err == nil {
			return plans, nil
		}
		lastErr = err
		prompt = structurePrompt(request) + "\n\n再次提醒：上一次输出不是合法 JSON 或包含了 JSON 以外的内容。只输出一个 JSON 对象，line 必须逐字来自草图中的 @行号。"
	}
	return nil, lastErr
}

const structureSketchBudget = 16 << 10

// sketchLine is one numbered entry in the structural sketch.
type sketchLine struct {
	line    int
	offset  int
	heading bool
}

// BuildStructureSketch compresses a long document into a numbered map of
// headings and paragraph openings after fromOffset, sampled to fit the local
// model's context. It returns the sketch and a line→position lookup used to
// verify and locate the model's chosen boundaries.
func BuildStructureSketch(markdown string, fromOffset int) (string, map[int]sketchLine) {
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
		line    int
		offset  int
		marker  string
		text    string
		heading bool
	}
	entries := make([]entry, 0, 512)
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		switch node.Kind() {
		case ast.KindHeading:
			heading := node.(*ast.Heading)
			title := strings.TrimSpace(string(heading.Text(source)))
			if title == "" || heading.Lines().Len() == 0 {
				continue
			}
			line := lineOf(heading.Lines().At(0).Start)
			offset := lineStarts[line-1] // line start, including the # prefix
			if offset < fromOffset {
				continue
			}
			entries = append(entries, entry{line: line, offset: offset, marker: fmt.Sprintf("H%d", heading.Level), text: title, heading: true})
		case ast.KindParagraph:
			if node.Lines().Len() == 0 {
				continue
			}
			segment := node.Lines().At(0)
			if segment.Start < fromOffset {
				continue
			}
			first := strings.TrimSpace(string(segment.Value(source)))
			if len([]rune(first)) < 8 || strings.HasPrefix(first, "![]") || strings.HasPrefix(first, "|") {
				continue
			}
			runes := []rune(first)
			if len(runes) > 90 {
				first = string(runes[:90]) + "…"
			}
			entries = append(entries, entry{line: lineOf(segment.Start), offset: segment.Start, marker: "P", text: first})
		}
	}

	build := func(stride int) (string, map[int]sketchLine) {
		var out strings.Builder
		lines := make(map[int]sketchLine, len(entries))
		last := len(entries) - 1
		for index, item := range entries {
			if item.marker == "P" && stride > 1 && index%stride != 0 && index != last {
				continue
			}
			fmt.Fprintf(&out, "%s @%d %s\n", item.marker, item.line, item.text)
			lines[item.line] = sketchLine{line: item.line, offset: item.offset, heading: item.heading}
		}
		return out.String(), lines
	}
	sketch, lines := build(1)
	for stride := 2; len(sketch) > structureSketchBudget && stride <= 16; stride++ {
		sketch, lines = build(stride)
	}
	if len(sketch) > structureSketchBudget {
		sketch = sketch[:structureSketchBudget]
	}
	return sketch, lines
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

// parseBookTOC reads the Contents region (part headings plus table rows) into
// the book's true, ordered chapter list. Front-matter H1 sections such as
// Introduction/Preface are prepended when they lead the body.
func parseBookTOC(markdown string, tocStart, tocEnd int) []string {
	lines := strings.Split(markdown, "\n")
	lineStarts := lineStartOffsets([]byte(markdown))
	footnote := regexp.MustCompile(`\[\d+\]\s*$`)

	var entries []string
	part := ""
	for index, text := range lines {
		offset := lineStarts[index]
		if offset <= tocStart || offset >= tocEnd {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if heading := atxHeadingText(trimmed); heading != "" {
			if !isTOCHeading(heading) {
				part = footnote.ReplaceAllString(heading, "")
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(trimmed, "|"))
		if cell == "" || strings.Trim(cell, "-") == "" || strings.HasPrefix(cell, "By ") {
			continue
		}
		label, title := cell, ""
		if colon := strings.Index(cell, ":"); colon > 0 {
			label, title = strings.TrimSpace(cell[:colon]), strings.TrimSpace(cell[colon+1:])
		}
		display := label
		if title != "" {
			display = label + ": " + title
		}
		if part != "" {
			display = part + " · " + display
		}
		entries = append(entries, display)
	}

	// Front-matter H1s (Introduction/Preface) lead the body but sit outside
	// the table-formatted TOC; collect them until the first other H1.
	var front []string
	for index, text := range lines {
		offset := lineStarts[index]
		if offset < tocEnd {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmed, "# ") {
			continue
		}
		title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		if !ancillaryPattern.MatchString(title) {
			break
		}
		front = append(front, title)
	}
	return append(front, entries...)
}

// tocRegionBounds locates the Contents/目录 heading offset and the offset of
// the next level-1 heading that terminates the TOC region. tocEnd is 0 when
// no region terminator exists.
func tocRegionBounds(markdown string) (tocStart, tocEnd int) {
	lines := strings.Split(markdown, "\n")
	lineStarts := lineStartOffsets([]byte(markdown))
	tocStart, tocEnd = -1, 0
	for index, text := range lines {
		trimmed := strings.TrimSpace(text)
		if tocStart < 0 {
			if title := atxHeadingText(trimmed); title != "" && isTOCHeading(title) {
				tocStart = lineStarts[index]
			}
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			tocEnd = lineStarts[index]
			break
		}
	}
	if tocStart < 0 {
		return -1, 0
	}
	return tocStart, tocEnd
}

func structurePrompt(request PlanRequest) string {
	var b strings.Builder
	b.WriteString("任务：把一本 PDF 转换来的 Markdown 书切成阅读友好的章节。这是机械的文本定位任务，不是关于这本书的问答；禁止输出外部知识、链接或评论。\n\n")
	if len(request.Entries) > 0 {
		b.WriteString("这本书的真实目录如下（编号固定，按顺序）：\n")
		for index, entry := range request.Entries {
			fmt.Fprintf(&b, "%d. %s\n", index+1, entry)
		}
		b.WriteString("\n为目录中的每个条目给出它在正文中开始的那一行的行号。\n\n")
	} else {
		b.WriteString("这本书没有可用目录；请自己划分 2-30 个章节并给出可读标题。\n\n")
	}
	b.WriteString("正文结构草图（@ 后是行号；H 行是标题，P 行是段落开头，可能按间隔抽样）：\n")
	b.WriteString(request.Sketch)
	b.WriteString(`
输出示例（仅演示 JSON 格式，不要复制示例文字）：
{"chapters":[{"chapter":1,"line":136},{"chapter":2,"line":205}]}
` + "无目录时输出：" + `
{"chapters":[{"title":"章节标题","line":205},{"title":"章节标题","line":388}]}

硬性规则：
1. line 必须逐字来自草图中出现的 @行号，且严格递增。
2. 章节开始通常对应标题行，或叙述视角/场景的明显切换处。
3. 目录条目多时不要遗漏；不确定就选最接近的段落开头，宁粗勿缺。
4. 不要输出 ` + "```" + `、解释、链接或任何 JSON 以外的字符。`)
	return b.String()
}

func parseChapterPlans(raw string, entries []string) ([]ChapterPlan, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "```")
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
	if start := strings.IndexByte(raw, '{'); start >= 0 {
		if end := strings.LastIndexByte(raw, '}'); end >= start {
			raw = raw[start : end+1]
		}
	}
	var payload struct {
		Chapters []struct {
			Chapter int    `json:"chapter"`
			Title   string `json:"title"`
			Line    int    `json:"line"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode chapter plan: %w (model output: %.300s)", err, raw)
	}
	plans := make([]ChapterPlan, 0, len(payload.Chapters))
	for _, chapter := range payload.Chapters {
		if chapter.Line <= 0 {
			continue
		}
		title := sanitizePlanText(chapter.Title, 120)
		if len(entries) > 0 {
			if chapter.Chapter < 1 || chapter.Chapter > len(entries) {
				continue
			}
			title = entries[chapter.Chapter-1]
		} else if title == "" {
			continue
		}
		plans = append(plans, ChapterPlan{Title: title, Line: chapter.Line})
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
