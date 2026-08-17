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

func TestPostprocessMarkdownUsesTOCWhenBodyDropsChapterNumbers(t *testing.T) {
	markdown := `## Table of Contents

第零章、必读系列 1.2
学习算法和刷题的框架思维 1.2.1
第一章、动态规划系列 1.3
动态规划解题套路框架 1.3.1
第二章、数据结构系列 2.1
算法学习之路 2.1.1
三章、技术文章系列 3.1
Linux的进程、线程、文件描述符是什么 3.1.1

# 学习算法和刷题的思路指南

intro

## 动态规划系列

dp body

## 数据结构系列

data body

## Linux的进程、线程、⽂件描述符是什么

linux body
`
	result := PostprocessMarkdown(markdown, "pdf-toc.md")
	if len(result.Chapters) != 4 {
		t.Fatalf("TOC chapters=%d: %+v", len(result.Chapters), result.Chapters)
	}
	wantFiles := []string{
		"pdf-toc-chapter-000.md", "pdf-toc-chapter-001.md", "pdf-toc-chapter-002.md", "pdf-toc-chapter-003.md",
	}
	for index, filename := range wantFiles {
		if result.Chapters[index].Filename != filename || !strings.Contains(result.IndexMarkdown, "]("+filename+")") {
			t.Fatalf("TOC chapter %d=%+v index=%q", index, result.Chapters[index], result.IndexMarkdown)
		}
	}
	if result.Chapters[0].Title != "第零章、必读系列" || !strings.Contains(result.Chapters[0].Markdown, "## 学习算法和刷题的思路指南") {
		t.Fatalf("first TOC fallback did not preserve its article heading: %+v", result.Chapters[0])
	}
	if result.Chapters[1].Title != "第一章、动态规划系列" || strings.Contains(result.Chapters[1].Markdown, "## 动态规划系列") {
		t.Fatalf("exact TOC chapter heading was not normalized: %+v", result.Chapters[1])
	}
	if result.Chapters[3].Title != "第三章、技术文章系列" || !strings.Contains(result.Chapters[3].Markdown, "## Linux的进程、线程、⽂件描述符是什么") {
		t.Fatalf("TOC article fallback was not preserved: %+v", result.Chapters[3])
	}
}

func TestPostprocessMarkdownSplitsRepeatedEnglishContentsAndSkipsChapterSummaries(t *testing.T) {
	markdown := `# FOOLED BY RANDOMNESS

## CONTENTS

Preface and Acknowledgments xiii
One: First Chance 11
Two: Alternative History 26

## CONTENTS

Three: Meditation on History 40
Four: Scientific Intellectual 60
Epilogue: Solon Told You So 196

# PREFACE AND ACKNOWLEDGMENTS

preface body

# CHAPTER SUMMARIES

## First Chance

summary one

## Alternative History

summary two

# MOSQUES IN THE CLOUDS

prologue body

# FIRST CHANCE

chapter one body

# ALTERNATIVE HISTORY

chapter two body

## MEDITATION ON HISTORY

chapter three body

# SCIENTIFIC INTELLECTUAL

chapter four body

# EPILOGUE: SOLON TOLD YOU SO

epilogue body
`
	result := PostprocessMarkdown(markdown, "fooled.md")
	if len(result.Chapters) != 6 {
		t.Fatalf("English TOC chapters=%d: %+v", len(result.Chapters), result.Chapters)
	}
	wantTitles := []string{
		"PREFACE AND ACKNOWLEDGMENTS",
		"Chapter One: First Chance",
		"Chapter Two: Alternative History",
		"Chapter Three: Meditation on History",
		"Chapter Four: Scientific Intellectual",
		"EPILOGUE: SOLON TOLD YOU SO",
	}
	wantFiles := []string{
		"fooled-part-introduction.md", "fooled-chapter-001.md", "fooled-chapter-002.md",
		"fooled-chapter-003.md", "fooled-chapter-004.md", "fooled-part-afterword.md",
	}
	for index := range wantTitles {
		if result.Chapters[index].Title != wantTitles[index] || result.Chapters[index].Filename != wantFiles[index] {
			t.Fatalf("English chapter %d=%+v", index, result.Chapters[index])
		}
	}
	if strings.Contains(result.Chapters[1].Markdown, "summary one") || !strings.Contains(result.Chapters[1].Markdown, "chapter one body") {
		t.Fatalf("chapter summary was selected instead of body: %s", result.Chapters[1].Markdown)
	}
	if !strings.Contains(result.Chapters[0].Markdown, "summary one") || !strings.Contains(result.Chapters[0].Markdown, "prologue body") {
		t.Fatalf("front matter before Chapter One was dropped: %s", result.Chapters[0].Markdown)
	}
}

func TestPostprocessMarkdownRequiresTwoMatchedTOCChapters(t *testing.T) {
	cases := []string{
		"## Table of Contents\n\n第一章、Only One 1.1\n第二章、Missing 2.1\n\n## Only One\n\nbody\n",
		"## Table of Contents\n\n第零章、Missing Prelude 0.1\n第一章、Only One 1.1\n\n# Arbitrary First Heading\n\nintro\n\n## Only One\n\nbody\n",
	}
	for _, markdown := range cases {
		result := PostprocessMarkdown(markdown, "pdf-id.md")
		if result.IndexMarkdown != markdown || len(result.Chapters) != 0 {
			t.Fatalf("single trusted TOC match should not split: %+v", result)
		}
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
	for input, want := range map[string]int{"12": 12, "１２": 12, "十二": 12, "二十一": 21, "一百零二": 102, "IV": 4, "Fourteen": 14} {
		got, ok := parseChapterNumber(input)
		if !ok || got != want {
			t.Fatalf("parseChapterNumber(%q)=(%d,%v), want %d", input, got, ok, want)
		}
	}
}

// O'Reilly-style dot-leader TOCs ("1. Title.......... 12") parse as Markdown
// ordered lists: goldmark consumes the "1." marker, so the old paragraph-only
// scan found zero chapters and the whole book stayed one blob.
func TestPostprocessMarkdownSplitsDotLeaderOrderedListTOC(t *testing.T) {
	markdown := `# Sample Book

## Table of Contents

Preface.................... xi
1. Getting Started.................... 1
Item 1: First Lesson 1
Item 2: Second Lesson 7
2. Going Deeper.................... 25
Item 3: Third Lesson 25

## Preface

` + strings.Repeat("preface body ", 80) + `

# Getting Started

## Item 1: First Lesson

` + strings.Repeat("chapter one body ", 200) + `

## Item 2: Second Lesson

` + strings.Repeat("more chapter one ", 120) + `

# Going Deeper

## Item 3: Third Lesson

` + strings.Repeat("chapter two body ", 200) + `
`
	result := PostprocessMarkdown(markdown, "book.md")
	if len(result.Chapters) < 2 {
		t.Fatalf("expected dot-leader list TOC to split, got %d chapters", len(result.Chapters))
	}
	titles := make([]string, len(result.Chapters))
	for i, chapter := range result.Chapters {
		titles[i] = chapter.Title
	}
	if !strings.Contains(strings.Join(titles, "|"), "Chapter 1: Getting Started") {
		t.Fatalf("missing chapter 1 in %v", titles)
	}
	if !strings.Contains(strings.Join(titles, "|"), "Chapter 2: Going Deeper") {
		t.Fatalf("missing chapter 2 in %v", titles)
	}
	var ch1, ch2 string
	for _, chapter := range result.Chapters {
		if chapter.Title == "Chapter 1: Getting Started" {
			ch1 = chapter.Markdown
		}
		if chapter.Title == "Chapter 2: Going Deeper" {
			ch2 = chapter.Markdown
		}
	}
	if !strings.Contains(ch1, "chapter one body") {
		t.Fatal("chapter 1 lost its body")
	}
	if !strings.Contains(ch2, "chapter two body") {
		t.Fatal("chapter 2 lost its body")
	}
}

// Chinese dot-leader TOCs glue the page number directly to the leader with no
// space (第1章 标题....3), and section entries (13.5 本章小结....448) must not
// be misread as "Chapter 13" when "13." looks like a chapter number.
func TestPostprocessMarkdownSplitsChineseDotLeaderTOCWithoutSpaces(t *testing.T) {
	markdown := `# 样书

## 目录

第1部分 简介....1
第1章 温故而知新....3
1.1 从 Hello 说起....4
1.2 本章小结....9
第2章 编译和链接....37
2.1 被隐藏的过程....38
2.2 本章小结....53

## 序言

` + strings.Repeat("序言内容 ", 60) + `

# 温故而知新

` + strings.Repeat("第一章正文 ", 200) + `

# 编译和链接

` + strings.Repeat("第二章正文 ", 200) + `
`
	result := PostprocessMarkdown(markdown, "book.md")
	var ch1, ch2 string
	for _, chapter := range result.Chapters {
		if chapter.Title == "第1章、温故而知新" {
			ch1 = chapter.Markdown
		}
		if chapter.Title == "第2章、编译和链接" {
			ch2 = chapter.Markdown
		}
		if strings.Contains(chapter.Title, "本章小结") {
			t.Fatalf("section entry became a chapter: %q", chapter.Title)
		}
	}
	if ch1 == "" || ch2 == "" {
		t.Fatalf("expected 第1章/第2章 boundaries, got %+v", result.Chapters)
	}
	if !strings.Contains(ch1, "第一章正文") || !strings.Contains(ch2, "第二章正文") {
		t.Fatal("chapter bodies missing")
	}
}
