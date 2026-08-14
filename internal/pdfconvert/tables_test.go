package pdfconvert

import (
	"strings"
	"testing"
)

func TestNormalizeHTMLTablesConvertsSimpleTableToGFM(t *testing.T) {
	input := "Before\n\n<table><tr><td>操作系统</td><td>多 Agent 系统</td></tr><tr><td>fork | wait</td><td><code>spawn_subagent</code></td></tr></table>\n\nAfter\n"
	got := normalizeHTMLTables(input)
	want := "Before\n\n| 操作系统 | 多 Agent 系统 |\n| --- | --- |\n| fork \\| wait | `spawn_subagent` |\n\nAfter\n"
	if got != want {
		t.Fatalf("table conversion mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNormalizeHTMLTablesFlattensRowAndColumnSpans(t *testing.T) {
	input := `<table><tr><td rowspan="2">实验</td><td colspan="2">成功率</td></tr><tr><td>基线</td><td>实验组</td></tr></table>`
	got := normalizeHTMLTables(input)
	want := "| 实验 | 成功率 | 成功率 |\n| --- | --- | --- |\n| 实验 | 基线 | 实验组 |"
	if got != want {
		t.Fatalf("span conversion mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNormalizeHTMLTablesDoesNotRewriteCodeOrQuotedExamples(t *testing.T) {
	input := "```html\n<table><tr><td>code</td></tr></table>\n```\n\n> <table><tr><td>quote</td></tr></table>\n"
	got := normalizeHTMLTables(input)
	if got != input {
		t.Fatalf("nested table examples were rewritten:\n%s", got)
	}
}

func TestPostprocessMarkdownNormalizesTablesBeforeChapterSplit(t *testing.T) {
	input := "# Book\n\n## 第 1 章 One\n\n<table><tr><td>A</td><td>B</td></tr><tr><td>1</td><td>2</td></tr></table>\n\n## 第 2 章 Two\n\nbody\n"
	result := PostprocessMarkdown(input, "book.md")
	if len(result.Chapters) != 2 || !strings.Contains(result.Chapters[0].Markdown, "| A | B |") || strings.Contains(result.Chapters[0].Markdown, "<table>") {
		t.Fatalf("table was not normalized before split: %+v", result.Chapters)
	}
}
