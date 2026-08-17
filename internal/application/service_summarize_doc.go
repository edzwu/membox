package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"membox/internal/domain/catalog"
)

// Full-document summarization: dynamic-window map-reduce over the local mmd
// model. Every window is derived from the model's context size — nothing is
// hard-coded to a fixed text length.
//
// Strategy (per design):
//
//	W          model context window (tokens), qwen3:14b → 32768
//	α          safety factor 0.8 → W_usable = W × α
//	T          target summary length (runes) → 1000
//	T_token    output reserve ≈ T × 1.5 tokens
//	W_seg      per-call input budget = W_usable − T_token
//	depth      recursive merge layers, max 3; window ×1.5 per layer
const (
	summarizeWindowTokens   = 32768
	summarizeSafetyFactor   = 0.8
	summarizeTargetRunes    = 1000
	summarizeOutputTokens   = summarizeTargetRunes * 3 / 2
	summarizeMaxDepth       = 3
	summarizeMergeWindowMul = 1.5
	summarizeSegTargetRatio = 0.6
)

// SummarizeSegmentBudgetTokens is the per-call input token budget.
func SummarizeSegmentBudgetTokens() int {
	// Integer math of W × α (α = 8/10) − output reserve.
	return summarizeWindowTokens*8/10 - summarizeOutputTokens
}

// SummarizeProgress reports pipeline stages for streaming UIs.
type SummarizeProgress struct {
	Stage string `json:"stage"` // split | segment | merge | final | save
	Index int    `json:"index,omitempty"`
	Total int    `json:"total,omitempty"`
	Depth int    `json:"depth,omitempty"`
}

// SummarizeDocumentResult identifies the stored summary note.
type SummarizeDocumentResult struct {
	NoteID   string
	Path     string
	Title    string
	Segments int
	Chars    int
	// Existing is true when a prior summary note was reused without any
	// model work (idempotent re-trigger).
	Existing bool
}

type summarizeSegment struct {
	Text    string
	Anchors []string
}

// estimateTokens approximates model tokens: CJK runes ≈1.5 tokens each,
// other characters ≈4 per token. Deliberately conservative.
func estimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk*3/2 + other/4 + 1
}

func runeCount(text string) int { return utf8.RuneCountInString(text) }

func truncateRunes(text string, limit int) string {
	if runeCount(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

// splitIntoSegments cuts text on blank-line paragraph boundaries, keeping
// each segment under budgetTokens. Oversized paragraphs fall back to sentence
// boundaries, then to a hard rune cut. Headings are recorded as anchors.
func splitIntoSegments(text string, budgetTokens int) []summarizeSegment {
	blocks := strings.Split(text, "\n\n")
	var segments []summarizeSegment
	var current strings.Builder
	var anchors []string
	flush := func() {
		chunk := strings.TrimSpace(current.String())
		current.Reset()
		if chunk == "" {
			return
		}
		segments = append(segments, summarizeSegment{Text: chunk, Anchors: anchors})
		anchors = nil
	}
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		if estimateTokens(trimmed) > budgetTokens {
			flush()
			for _, part := range splitOversizedBlock(trimmed, budgetTokens) {
				segments = append(segments, summarizeSegment{Text: part, Anchors: collectHeadings(part)})
			}
			continue
		}
		if current.Len() > 0 && estimateTokens(current.String()+"\n\n"+trimmed) > budgetTokens {
			flush()
		}
		anchors = append(anchors, collectHeadings(trimmed)...)
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(trimmed)
	}
	flush()
	return segments
}

// splitOversizedBlock breaks one huge paragraph by sentence, then by runes.
func splitOversizedBlock(block string, budgetTokens int) []string {
	hardRuneBudget := budgetTokens * 2 / 3 // ≈ CJK runes per token budget
	if hardRuneBudget < 200 {
		hardRuneBudget = 200
	}
	sentences := splitSentences(block)
	var parts []string
	var current strings.Builder
	flush := func() {
		chunk := strings.TrimSpace(current.String())
		current.Reset()
		if chunk != "" {
			parts = append(parts, chunk)
		}
	}
	for _, sentence := range sentences {
		if estimateTokens(sentence) > budgetTokens {
			flush()
			for runeCount(sentence) > hardRuneBudget {
				runes := []rune(sentence)
				parts = append(parts, string(runes[:hardRuneBudget]))
				sentence = string(runes[hardRuneBudget:])
			}
			if strings.TrimSpace(sentence) != "" {
				parts = append(parts, strings.TrimSpace(sentence))
			}
			continue
		}
		if current.Len() > 0 && estimateTokens(current.String()+sentence) > budgetTokens {
			flush()
		}
		current.WriteString(sentence)
	}
	flush()
	return parts
}

func splitSentences(text string) []string {
	var sentences []string
	start := 0
	for i, r := range text {
		if strings.ContainsRune("。！？!?\n", r) {
			end := i + utf8.RuneLen(r)
			sentences = append(sentences, text[start:end])
			start = end
		}
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

func collectHeadings(block string) []string {
	var headings []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			headings = append(headings, strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
		}
	}
	return headings
}

func dedupeAnchors(all []string, cap int) []string {
	seen := map[string]bool{}
	var out []string
	for _, anchor := range all {
		if anchor == "" || seen[anchor] {
			continue
		}
		seen[anchor] = true
		out = append(out, anchor)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// findSummaryNote locates an existing summary note for the document: an
// active outgoing link whose Markdown front matter carries kind "summary"
// and this document's source_document_id. The newest match wins.
func (s *Service) findSummaryNote(ctx context.Context, document *catalog.Document) (noteID, notePath, noteTitle string, found bool) {
	_, graph, err := s.GetDocumentGraph(ctx, string(document.ID))
	if err != nil {
		return "", "", "", false
	}
	marker := `source_document_id: "` + string(document.ID) + `"`
	var best *catalog.Document
	var bestLinkPath string
	for _, link := range graph.Outgoing {
		candidate := link.Document
		if candidate == nil || candidate.Status != catalog.DocumentActive {
			continue
		}
		if candidate.Index.MediaType != "text/markdown" {
			continue
		}
		body, readErr := s.ReadDocumentText(ctx, string(candidate.ID))
		if readErr != nil {
			continue
		}
		head := string(body)
		if len(head) > 2048 {
			head = head[:2048]
		}
		if !strings.Contains(head, marker) || !strings.Contains(head, `kind: "summary"`) {
			continue
		}
		if best == nil || candidate.CreatedAt.After(best.CreatedAt) {
			best = candidate
			bestLinkPath = link.Path
		}
	}
	if best == nil {
		return "", "", "", false
	}
	return string(best.ID), bestLinkPath, best.Index.Title, true
}

// SummarizeDocument map-reduces the document body into a ≤1000-rune Chinese
// Markdown summary via the local model, stores it as a note linked to the
// original document, and streams stage progress. Unless force is set, an
// existing summary note is reused without any model calls.
func (s *Service) SummarizeDocument(
	ctx context.Context,
	selector string,
	force bool,
	complete func(context.Context, string) (string, error),
	progress func(SummarizeProgress),
) (SummarizeDocumentResult, error) {
	if complete == nil {
		return SummarizeDocumentResult{}, errors.New("local model completer is required")
	}
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return SummarizeDocumentResult{}, err
	}
	if !force {
		if noteID, notePath, noteTitle, found := s.findSummaryNote(ctx, document); found {
			return SummarizeDocumentResult{NoteID: noteID, Path: notePath, Title: noteTitle, Existing: true}, nil
		}
	}
	body, err := s.ReadDocumentText(ctx, string(document.ID))
	if err != nil {
		return SummarizeDocumentResult{}, err
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return SummarizeDocumentResult{}, errors.New("document body is empty")
	}
	title := strings.TrimSpace(document.Index.Title)
	if title == "" {
		title = "未命名文档"
	}
	emit := func(p SummarizeProgress) {
		if progress != nil {
			progress(p)
		}
	}

	budget := SummarizeSegmentBudgetTokens()
	var condensed string
	segmentCount := 1
	var anchors []string

	if estimateTokens(text) <= budget {
		condensed = text
		anchors = collectHeadings(text)
	} else {
		segments := splitIntoSegments(text, budget)
		segmentCount = len(segments)
		emit(SummarizeProgress{Stage: "split", Total: segmentCount})
		perSeg := int(float64(summarizeTargetRunes)*summarizeSegTargetRatio/float64(segmentCount)) + 1
		if perSeg < 50 {
			perSeg = 50
		}
		summaries := make([]string, 0, segmentCount)
		for i, segment := range segments {
			emit(SummarizeProgress{Stage: "segment", Index: i + 1, Total: segmentCount})
			anchors = append(anchors, segment.Anchors...)
			summary, callErr := complete(ctx, buildSegmentPrompt(title, i+1, segmentCount, perSeg, segment.Text))
			if callErr != nil {
				return SummarizeDocumentResult{}, fmt.Errorf("segment %d/%d: %w", i+1, segmentCount, callErr)
			}
			summaries = append(summaries, strings.TrimSpace(summary))
		}
		condensed = strings.Join(summaries, "\n\n")
	}

	// Recursive merge until the condensed material fits the target budget.
	depth := 1
	for runeCount(condensed) > summarizeTargetRunes && depth <= summarizeMaxDepth {
		emit(SummarizeProgress{Stage: "merge", Depth: depth})
		mergeBudget := int(float64(budget) * summarizeMergeWindowMul)
		if estimateTokens(condensed) <= mergeBudget {
			merged, callErr := complete(ctx, buildMergePrompt(condensed))
			if callErr != nil {
				return SummarizeDocumentResult{}, fmt.Errorf("merge depth %d: %w", depth, callErr)
			}
			condensed = strings.TrimSpace(merged)
		} else {
			chunks := splitIntoSegments(condensed, mergeBudget)
			mergedParts := make([]string, 0, len(chunks))
			for i, chunk := range chunks {
				emit(SummarizeProgress{Stage: "merge", Index: i + 1, Total: len(chunks), Depth: depth})
				merged, callErr := complete(ctx, buildMergePrompt(chunk.Text))
				if callErr != nil {
					return SummarizeDocumentResult{}, fmt.Errorf("merge depth %d chunk %d: %w", depth, i+1, callErr)
				}
				mergedParts = append(mergedParts, strings.TrimSpace(merged))
			}
			condensed = strings.Join(mergedParts, "\n\n")
		}
		depth++
	}

	// Final formatting pass: structured Markdown ≤ T runes.
	emit(SummarizeProgress{Stage: "final"})
	final, err := complete(ctx, buildFinalPrompt(title, dedupeAnchors(anchors, 12), condensed))
	if err != nil {
		return SummarizeDocumentResult{}, fmt.Errorf("final pass: %w", err)
	}
	final = strings.TrimSpace(final)
	if runeCount(final) > summarizeTargetRunes*6/5 {
		compress, callErr := complete(ctx, buildMergePrompt(final))
		if callErr == nil && strings.TrimSpace(compress) != "" {
			final = strings.TrimSpace(compress)
		}
	}
	if runeCount(final) > summarizeTargetRunes*6/5 {
		final = truncateRunes(final, summarizeTargetRunes) + "…"
	}

	// Persist as a note linked to the original document.
	emit(SummarizeProgress{Stage: "save"})
	noteTitle := "摘要 · " + truncateRunes(title, 40)
	noteBody := buildSummaryNoteBody(string(document.ID), title, final)
	created, err := s.CreateNote(ctx, CreateNoteOptions{
		Title:        noteTitle,
		Body:         noteBody,
		FromSelector: string(document.ID),
	})
	if err != nil {
		return SummarizeDocumentResult{}, fmt.Errorf("saving summary note: %w", err)
	}
	return SummarizeDocumentResult{
		NoteID:   string(created.Document.ID),
		Path:     created.Path,
		Title:    noteTitle,
		Segments: segmentCount,
		Chars:    runeCount(final),
	}, nil
}

func buildSegmentPrompt(title string, index, total, targetRunes int, segment string) string {
	return fmt.Sprintf(`你在为长文档做分段摘要（第 %d/%d 段）。文档标题：《%s》。
要求：
- 用中文提炼本段核心内容，不超过 %d 字
- 保留关键术语、名称、代码标识符
- 只输出摘要本身，不要解释你在做什么

正文：
%s`, index, total, title, targetRunes, segment)
}

func buildMergePrompt(text string) string {
	return fmt.Sprintf(`以下是一篇长文档的摘要素材，仍超出目标长度。请进一步压缩合并，保留最重要的观点、结论与关键术语，输出不超过 %d 字的连贯中文文本（不要分点、不要标题，保持信息密度，只输出压缩后的文本）。

素材：
%s`, summarizeTargetRunes, text)
}

func buildFinalPrompt(title string, anchors []string, condensed string) string {
	anchorLine := "（无可用章节锚点）"
	if len(anchors) > 0 {
		anchorLine = strings.Join(anchors, "；")
	}
	return fmt.Sprintf(`基于以下压缩素材，为文档《%s》输出一篇中文 Markdown 摘要。
结构（严格遵守）：
# 全文摘要
## 核心观点
- 3–6 条要点
## 关键细节
> 引用块呈现关键论据/数据，尽量带「详见原文：%s」形式的锚点
## 总结
一两句话收束。

要求：
- 总字数（不含 Markdown 标记）不超过 %d 字
- 只输出 Markdown 本身，不要解释

可用章节锚点：%s

压缩素材：
%s`, title, "<章节或关键词>", summarizeTargetRunes, anchorLine, condensed)
}

func buildSummaryNoteBody(sourceID, sourceTitle, summary string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("kind: \"summary\"\n")
	b.WriteString(fmt.Sprintf("source_document_id: \"%s\"\n", sourceID))
	b.WriteString("model: \"mmd-local\"\n")
	b.WriteString("---\n\n")
	b.WriteString("# 摘要 · ")
	b.WriteString(sourceTitle)
	b.WriteString("\n\n")
	b.WriteString("> 原文：[")
	b.WriteString(sourceTitle)
	b.WriteString("](?id=")
	b.WriteString(sourceID)
	b.WriteString(")\n\n")
	b.WriteString(summary)
	b.WriteString("\n")
	return b.String()
}
