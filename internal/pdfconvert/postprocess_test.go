package pdfconvert

import (
	"strings"
	"testing"
)

func TestPostprocessMarkdownSplitsLongBookIntoIndexAndChapters(t *testing.T) {
	markdown := `# Agent Book

## 目录

第 1 章 ... 1

## 引言

intro body

## 第 1 章 Start

first body

` + "```python\n# Chapter 99 Fake\n## 第 99 章 Fake\n```\n\n" + `## 第二章 Context

second body

# 后记

afterword body
`
	result := PostprocessMarkdown(markdown, "pdf-id.md")
	if len(result.Chapters) != 4 {
		t.Fatalf("chapters=%d: %+v", len(result.Chapters), result.Chapters)
	}
	wantFiles := []string{
		"pdf-id-part-introduction.md",
		"pdf-id-chapter-001.md",
		"pdf-id-chapter-002.md",
		"pdf-id-part-afterword.md",
	}
	for index, filename := range wantFiles {
		if result.Chapters[index].Filename != filename {
			t.Fatalf("chapter %d filename=%q want=%q", index, result.Chapters[index].Filename, filename)
		}
		if !strings.Contains(result.IndexMarkdown, "]("+filename+")") {
			t.Fatalf("index does not link %s:\n%s", filename, result.IndexMarkdown)
		}
		if !strings.Contains(result.Chapters[index].Markdown, "[← Agent Book](pdf-id.md)") {
			t.Fatalf("chapter backlink missing:\n%s", result.Chapters[index].Markdown)
		}
	}
	if strings.Contains(result.IndexMarkdown, "first body") || strings.Contains(result.IndexMarkdown, "intro body") {
		t.Fatalf("index retained book content:\n%s", result.IndexMarkdown)
	}
	if !strings.Contains(result.Chapters[1].Markdown, "第 99 章 Fake") || strings.Contains(result.IndexMarkdown, "Chapter 99") {
		t.Fatalf("fenced headings were treated as chapter boundaries: %+v", result)
	}
}

func TestPostprocessMarkdownLeavesUnstructuredDocumentWhole(t *testing.T) {
	markdown := "# Short\n\n## 第 1 章 Only\n\nbody\n"
	result := PostprocessMarkdown(markdown, "pdf-id.md")
	if result.IndexMarkdown != markdown || len(result.Chapters) != 0 {
		t.Fatalf("single chapter should not be split: %+v", result)
	}
}

func TestPostprocessMarkdownUsesMarkdownAST(t *testing.T) {
	markdown := "Book\n====\n\nChapter 1 Start\n----------------\n\none\n\n> Chapter 99 Quoted\n> -----------------\n\n    Chapter 98 Indented\n    -------------------\n\nChapter 2 Finish\n----------------\n\ntwo\n"
	result := PostprocessMarkdown(markdown, "pdf-id.md")
	if len(result.Chapters) != 2 {
		t.Fatalf("AST chapter count=%d: %+v", len(result.Chapters), result.Chapters)
	}
	if result.Chapters[0].Title != "Chapter 1 Start" || !strings.Contains(result.Chapters[0].Markdown, "one") || !strings.Contains(result.Chapters[0].Markdown, "Chapter 99 Quoted") {
		t.Fatalf("unexpected first AST chapter: %+v", result.Chapters[0])
	}
	if result.Chapters[1].Title != "Chapter 2 Finish" {
		t.Fatalf("unexpected second AST chapter: %+v", result.Chapters[1])
	}
}

func TestParseChapterNumber(t *testing.T) {
	for input, want := range map[string]int{"12": 12, "１２": 12, "十二": 12, "二十一": 21, "一百零二": 102, "IV": 4} {
		got, ok := parseChapterNumber(input)
		if !ok || got != want {
			t.Fatalf("parseChapterNumber(%q)=(%d,%v), want %d", input, got, ok, want)
		}
	}
}
