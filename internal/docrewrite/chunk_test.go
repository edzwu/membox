package docrewrite

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitForRewriteRespectsBudgetAndHeadings(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\ntitle: t\n---\n\n")
	b.WriteString("# Book\n\nintro para\n\n")
	for i := 1; i <= 8; i++ {
		b.WriteString("## Section ")
		b.WriteString(strings.Repeat(string(rune('A'+i-1)), 20))
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat("正文内容若干。", 80))
		b.WriteString("\n\n")
	}
	md := b.String()
	plan := splitForRewrite(md, 800)
	if plan.FrontMatter == "" || !strings.Contains(plan.FrontMatter, "title:") {
		t.Fatalf("front matter missing: %q", plan.FrontMatter)
	}
	if len(plan.Parts) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(plan.Parts))
	}
	for i, p := range plan.Parts {
		if n := utf8.RuneCountInString(p); n > 900 {
			t.Fatalf("part %d too large: %d runes", i, n)
		}
	}
	joined := joinRewriteParts(plan.FrontMatter, plan.Parts)
	if !strings.HasPrefix(strings.TrimSpace(joined), "---") {
		t.Fatalf("joined lost front matter: %q", joined[:min(40, len(joined))])
	}
}

func TestSplitKeepsCodeFenceIntact(t *testing.T) {
	md := "## A\n\n```go\nfunc main() {\n\tfmt.Println(1)\n}\n```\n\n## B\n\nok\n"
	plan := splitForRewrite(md, 5000)
	if len(plan.Parts) != 1 {
		t.Fatalf("parts=%d", len(plan.Parts))
	}
	if !strings.Contains(plan.Parts[0], "```go") {
		t.Fatalf("fence broken: %s", plan.Parts[0])
	}
}

func TestPeelFrontMatter(t *testing.T) {
	fm, body := peelFrontMatter("---\na: 1\n---\n\n# Hi\n")
	if !strings.Contains(fm, "a: 1") || !strings.Contains(body, "# Hi") {
		t.Fatalf("fm=%q body=%q", fm, body)
	}
}

func TestSplitWithoutHeadingsUsesParagraphs(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(strings.Repeat("没有标题的段落内容。", 40))
		b.WriteString("\n\n")
	}
	md := b.String()
	plan := splitForRewrite(md, 600)
	if len(plan.Parts) < 3 {
		t.Fatalf("expected paragraph packing into multiple parts, got %d (runes=%d)",
			len(plan.Parts), utf8.RuneCountInString(md))
	}
	for i, p := range plan.Parts {
		if n := utf8.RuneCountInString(p); n > 750 {
			t.Fatalf("part %d too large: %d", i, n)
		}
	}
}

func TestSplitWithoutHeadingsKeepsFenceAtomic(t *testing.T) {
	code := "```go\n" + strings.Repeat("fmt.Println(1)\n", 5) + "```"
	md := strings.Repeat("前文。\n\n", 3) + code + "\n\n" + strings.Repeat("后文。\n\n", 3)
	plan := splitForRewrite(md, 2000)
	ok := false
	for _, p := range plan.Parts {
		if strings.Contains(p, "```go") && strings.Count(p, "```") >= 2 {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("code fence split across chunks: %+v", plan.Parts)
	}
}

func TestSplitHardWhenNoBlankLines(t *testing.T) {
	md := strings.Repeat("字", 5000)
	plan := splitForRewrite(md, 800)
	if len(plan.Parts) < 5 {
		t.Fatalf("hard split expected, got %d parts", len(plan.Parts))
	}
	for i, p := range plan.Parts {
		if n := utf8.RuneCountInString(p); n > 900 {
			t.Fatalf("part %d=%d runes", i, n)
		}
	}
}

func TestApplySeamReplacement(t *testing.T) {
	left := "AAA\n\n句子左半"
	right := "句子右半\n\nBBB"
	leftTail := "句子左半"
	rightHead := "句子右半"
	polished := "句子左半，接上。\n\n句子右半完整。"
	nl, nr, ok := applySeamReplacement(left, right, leftTail, rightHead, polished)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.Contains(nl, "AAA") || !strings.Contains(nr, "BBB") {
		t.Fatalf("lost body: %q | %q", nl, nr)
	}
	if !strings.Contains(nl+nr, "接上") {
		t.Fatalf("polish not applied: %q | %q", nl, nr)
	}
}

func TestTailHeadRunes(t *testing.T) {
	s := "abcdefghij"
	if got := tailRunes(s, 3); got != "hij" {
		t.Fatalf("tail=%q", got)
	}
	if got := headRunes(s, 3); got != "abc" {
		t.Fatalf("head=%q", got)
	}
}
