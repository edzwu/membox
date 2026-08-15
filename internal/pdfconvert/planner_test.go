package pdfconvert

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var unstructuredBook = `# LINUS TORVALDS

## Contents

## Birth of a NERD

| I |
| --- |
| II |

# Preface: The Meaning of Life I

` + strings.Repeat("Preface dialogue about the meaning of life. ", 30) + `

SETTING: This book has its origins in a late-model black Ford Expedition on Interstate 5.

` + strings.Repeat("The nerd story begins in Helsinki with a grandfather's Commodore. ", 30) + `

Years later the operating system chapters open with a frustrating Minix terminal.

` + strings.Repeat("Linux grows from a hobby into a revolution. ", 30) + `

# Epilogue: The Amusement Ride Ahead

` + strings.Repeat("Closing reflections on fame and open source. ", 30)

func fakePlanner(response string, err error) StructurePlanner {
	return LLMStructurePlanner{Complete: func(context.Context, string) (string, error) {
		return response, err
	}}
}

func TestLLMPlannerSplitsUnstructuredBook(t *testing.T) {
	plan := `{"chapters":[
		{"title":"Preface: The Meaning of Life I","anchor":"Preface: The Meaning of Life I"},
		{"title":"Part 1: Birth of a NERD","anchor":"SETTING: This book has its origins"},
		{"title":"Part 2: Birth of an Operating System","anchor":"Years later the operating system chapters open"},
		{"title":"Epilogue: The Amusement Ride Ahead","anchor":"Epilogue: The Amusement Ride Ahead"}
	]}`
	result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md", fakePlanner(plan, nil))
	if len(result.Chapters) != 4 {
		t.Fatalf("chapters=%d: %+v", len(result.Chapters), result.Chapters)
	}
	if result.Chapters[0].Title != "Preface: The Meaning of Life I" || strings.Count(result.Chapters[0].Markdown, "Preface: The Meaning of Life I") != 1 {
		t.Fatalf("preface boundary wrong (heading must appear only as generated title): %q", result.Chapters[0].Markdown[:200])
	}
	if !strings.Contains(result.Chapters[1].Markdown, "SETTING: This book") {
		t.Fatalf("paragraph anchor content was dropped: %q", result.Chapters[1].Markdown[:200])
	}
	if !strings.Contains(result.Chapters[2].Markdown, "Linux grows from a hobby") {
		t.Fatalf("part 2 content missing: %q", result.Chapters[2].Markdown[:200])
	}
	if result.Chapters[3].Filename != "book-chapter-003.md" {
		t.Fatalf("unexpected filename: %s", result.Chapters[3].Filename)
	}
	if !strings.Contains(result.IndexMarkdown, "Part 2: Birth of an Operating System") {
		t.Fatalf("index missing planned titles: %q", result.IndexMarkdown)
	}
}

func TestLLMPlannerKeepsSubstantialFrontMatter(t *testing.T) {
	markdown := "# Big Book\n\n## Contents\n\n" + strings.Repeat("A long publishing history and TOC block. ", 40) +
		"\n\nChapter One opens with a vivid scene on the road.\n\n" + strings.Repeat("First chapter body. ", 60) +
		"\n\nChapter Two continues the journey north.\n\n" + strings.Repeat("Second chapter body. ", 60)
	plan := `{"chapters":[
		{"title":"Chapter One","anchor":"Chapter One opens with a vivid scene"},
		{"title":"Chapter Two","anchor":"Chapter Two continues the journey"}
	]}`
	result := PostprocessMarkdownWithPlanner(context.Background(), markdown, "book.md", fakePlanner(plan, nil))
	if len(result.Chapters) != 3 || result.Chapters[0].Title != "Front Matter" {
		t.Fatalf("front matter not preserved: %+v", result.Chapters)
	}
	if !strings.Contains(result.Chapters[0].Markdown, "publishing history") {
		t.Fatalf("front matter content dropped: %q", result.Chapters[0].Markdown[:160])
	}
}

func TestLLMPlannerRejectsUnverifiableAnchors(t *testing.T) {
	plan := `{"chapters":[
		{"title":"Invented","anchor":"This sentence does not exist anywhere"},
		{"title":"Table row","anchor":"| I |"}
	]}`
	result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md", fakePlanner(plan, nil))
	if len(result.Chapters) != 0 || result.IndexMarkdown != unstructuredBook {
		t.Fatal("unverifiable plan must keep the document whole")
	}
}

func TestLLMPlannerErrorKeepsDocumentWhole(t *testing.T) {
	result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md",
		fakePlanner("", errors.New("mmd is not running")))
	if len(result.Chapters) != 0 || result.IndexMarkdown != unstructuredBook {
		t.Fatal("planner failure must fall back to the whole document")
	}
}

func TestDeterministicSplitWinsOverPlanner(t *testing.T) {
	markdown := "# Book\n\n## 第一章、开端\n\n" + strings.Repeat("正文。", 100) + "\n\n## 第二章、进展\n\n" + strings.Repeat("更多正文。", 100)
	called := false
	planner := LLMStructurePlanner{Complete: func(context.Context, string) (string, error) {
		called = true
		return "", nil
	}}
	result := PostprocessMarkdownWithPlanner(context.Background(), markdown, "book.md", planner)
	if called {
		t.Fatal("planner was invoked despite a successful deterministic split")
	}
	if len(result.Chapters) != 2 {
		t.Fatalf("deterministic chapters=%d", len(result.Chapters))
	}
}

func TestBuildStructureSketchCoversWholeDocument(t *testing.T) {
	var doc strings.Builder
	doc.WriteString("# Title\n\n")
	for index := 0; index < 400; index++ {
		doc.WriteString(strings.Repeat("A long paragraph body for sampling. ", 8))
		doc.WriteString("\n\n")
	}
	doc.WriteString("Final chapter opens here with a distinctive sentence.\n")
	sketch := BuildStructureSketch(doc.String())
	if len(sketch) > structureSketchBudget {
		t.Fatalf("sketch exceeds budget: %d", len(sketch))
	}
	if !strings.Contains(sketch, "H1 @1 Title") {
		t.Fatalf("heading missing from sketch: %q", sketch[:120])
	}
	if !strings.Contains(sketch, "Final chapter opens here") {
		t.Fatal("sampling dropped the end of the document")
	}
}

func TestParseChapterPlans(t *testing.T) {
	plans, err := parseChapterPlans("```json\n{\"chapters\":[{\"title\":\" 一 \",\"anchor\":\"Anchor text here\"}]}\n```")
	if err == nil || plans != nil {
		t.Fatalf("single chapter must be rejected: %+v err=%v", plans, err)
	}
	plans, err = parseChapterPlans(`{"chapters":[{"title":"One","anchor":"First anchor"},{"anchor":"Second anchor"}]}`)
	if err != nil || len(plans) != 2 || plans[1].Title != "Second anchor" {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
}
