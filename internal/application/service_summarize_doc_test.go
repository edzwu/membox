package application

import (
	"strings"
	"testing"
)

func TestEstimateTokensScalesWithContent(t *testing.T) {
	cjk := strings.Repeat("字", 1000)
	ascii := strings.Repeat("a", 1000)
	if got := estimateTokens(cjk); got < 1400 || got > 1600 {
		t.Fatalf("CJK estimate off: %d", got)
	}
	if got := estimateTokens(ascii); got < 200 || got > 300 {
		t.Fatalf("ASCII estimate off: %d", got)
	}
}

func TestSplitIntoSegmentsRespectsBudget(t *testing.T) {
	// 40 paragraphs × ~200 CJK chars each ≈ 40 × 300 tokens; budget 2000 tokens
	// must yield multiple segments, none over budget.
	var blocks []string
	for i := 0; i < 40; i++ {
		blocks = append(blocks, strings.Repeat("段", 200))
	}
	text := strings.Join(blocks, "\n\n")
	segments := splitIntoSegments(text, 2000)
	if len(segments) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(segments))
	}
	for i, segment := range segments {
		if tokens := estimateTokens(segment.Text); tokens > 2100 { // small slack for rounding
			t.Fatalf("segment %d exceeds budget: %d tokens", i, tokens)
		}
	}
	// Block content must survive; separators may differ.
	strip := func(s string) string { return strings.Join(strings.Fields(s), "") }
	joined := strings.Join(segmentTexts(segments), "")
	if strip(joined) != strip(text) {
		t.Fatalf("content lost: got %d runes, want %d", len([]rune(strip(joined))), len([]rune(strip(text))))
	}
}

func TestSplitOversizedBlockFallsBackToSentences(t *testing.T) {
	// One giant paragraph (no blank lines) must still split within budget.
	sentence := strings.Repeat("句", 100) + "。"
	block := strings.Repeat(sentence, 100) // ≈ 10100 CJK chars, one paragraph
	segments := splitIntoSegments(block, 2000)
	if len(segments) < 2 {
		t.Fatalf("expected sentence-level split, got %d segments", len(segments))
	}
	for i, segment := range segments {
		if tokens := estimateTokens(segment.Text); tokens > 2100 {
			t.Fatalf("segment %d exceeds budget: %d tokens", i, tokens)
		}
	}
}

func TestSplitCollectsHeadingAnchors(t *testing.T) {
	text := "# 第一章 入门\n\n" + strings.Repeat("内容", 100) + "\n\n## 小节\n\n" + strings.Repeat("更多", 100)
	segments := splitIntoSegments(text, 100000)
	if len(segments) != 1 {
		t.Fatalf("expected one segment, got %d", len(segments))
	}
	anchors := segments[0].Anchors
	if len(anchors) != 2 || anchors[0] != "第一章 入门" || anchors[1] != "小节" {
		t.Fatalf("anchors = %v", anchors)
	}
}

func segmentTexts(segments []summarizeSegment) []string {
	out := make([]string, len(segments))
	for i, segment := range segments {
		out[i] = segment.Text
	}
	return out
}
