package pdfconvert

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

# Notes

` + strings.Repeat("Closing reflections on fame and open source. ", 30)

// sketchLineFor finds the sketch line number containing prefix, so tests never
// hardcode line numbers.
func sketchLineFor(t *testing.T, sketch, prefix string) int {
	t.Helper()
	for _, line := range strings.Split(sketch, "\n") {
		if !strings.Contains(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "@") {
			if number, err := strconv.Atoi(fields[1][1:]); err == nil {
				return number
			}
		}
	}
	t.Fatalf("prefix %q not in sketch:\n%s", prefix, sketch)
	return 0
}

func TestLLMPlannerSplitsByBookTOC(t *testing.T) {
	tocStart, tocEnd := tocRegionBounds(unstructuredBook)
	sketch, _ := BuildStructureSketch(unstructuredBook, tocEnd)
	entries := parseBookTOC(unstructuredBook, tocStart, tocEnd)
	if len(entries) != 3 || entries[0] != "Preface: The Meaning of Life I" || entries[1] != "Birth of a NERD · I" || entries[2] != "Birth of a NERD · II" {
		t.Fatalf("TOC entries=%v", entries)
	}

	planJSON := fmt.Sprintf(`{"chapters":[
		{"chapter":1,"line":%d},
		{"chapter":2,"line":%d},
		{"chapter":3,"line":%d}
	]}`,
		sketchLineFor(t, sketch, "Preface: The Meaning of Life I"),
		sketchLineFor(t, sketch, "SETTING: This book has its origins"),
		sketchLineFor(t, sketch, "Years later the operating system"))

	planner := LLMStructurePlanner{Complete: func(context.Context, string) (string, error) { return planJSON, nil }}
	result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md", planner)
	if len(result.Chapters) != 3 {
		t.Fatalf("chapters=%d: %+v", len(result.Chapters), result.Chapters)
	}
	if result.Chapters[0].Title != "Preface: The Meaning of Life I" {
		t.Fatalf("chapter 0 title=%q", result.Chapters[0].Title)
	}
	if result.Chapters[1].Title != "Birth of a NERD · I" || !strings.Contains(result.Chapters[1].Markdown, "SETTING: This book") || !strings.Contains(result.Chapters[1].Markdown, "The nerd story begins") {
		t.Fatalf("part 1 chapter I wrong: %q", result.Chapters[1].Markdown[:160])
	}
	if !strings.Contains(result.Chapters[2].Markdown, "Linux grows from a hobby") {
		t.Fatalf("part 1 chapter II content missing: %q", result.Chapters[2].Markdown[:160])
	}
	if !strings.Contains(result.IndexMarkdown, "Birth of a NERD · II") {
		t.Fatalf("index missing TOC titles: %q", result.IndexMarkdown)
	}
}

func TestLLMPlannerRejectsUnknownAndUnorderedLines(t *testing.T) {
	_, tocEnd := tocRegionBounds(unstructuredBook)
	sketch, _ := BuildStructureSketch(unstructuredBook, tocEnd)
	good := sketchLineFor(t, sketch, "SETTING: This book has its origins")
	cases := []string{
		`{"chapters":[{"chapter":1,"line":99999},{"chapter":2,"line":3}]}`,                 // unknown line
		fmt.Sprintf(`{"chapters":[{"chapter":1,"line":%d},{"chapter":2,"line":1}]}`, good), // out of order / TOC region
	}
	for _, planJSON := range cases {
		planner := LLMStructurePlanner{Complete: func(context.Context, string) (string, error) { return planJSON, nil }}
		result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md", planner)
		if len(result.Chapters) != 0 || result.IndexMarkdown != unstructuredBook {
			t.Fatalf("invalid plan must keep the document whole: %s", planJSON)
		}
	}
}

func TestLLMPlannerWithoutTOCInventsTitles(t *testing.T) {
	markdown := "# Freeform\n\n" + strings.Repeat("Alpha narrative opens the document here. ", 40) +
		"\n\n" + strings.Repeat("Beta narrative takes over with a scene change. ", 40)
	sketch, _ := BuildStructureSketch(markdown, 0)
	planJSON := fmt.Sprintf(`{"chapters":[{"title":"Alpha","line":%d},{"title":"Beta","line":%d}]}`,
		sketchLineFor(t, sketch, "Alpha narrative opens"), sketchLineFor(t, sketch, "Beta narrative takes over"))
	planner := LLMStructurePlanner{Complete: func(context.Context, string) (string, error) { return planJSON, nil }}
	result := PostprocessMarkdownWithPlanner(context.Background(), markdown, "book.md", planner)
	if len(result.Chapters) != 2 || result.Chapters[0].Title != "Alpha" || result.Chapters[1].Title != "Beta" {
		t.Fatalf("chapters=%+v", result.Chapters)
	}
}

func TestLLMPlannerErrorKeepsDocumentWhole(t *testing.T) {
	planner := LLMStructurePlanner{Complete: func(context.Context, string) (string, error) {
		return "", errors.New("mmd is not running")
	}}
	result := PostprocessMarkdownWithPlanner(context.Background(), unstructuredBook, "book.md", planner)
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
	sketch, lines := BuildStructureSketch(doc.String(), 0)
	if len(sketch) > structureSketchBudget {
		t.Fatalf("sketch exceeds budget: %d", len(sketch))
	}
	if !strings.Contains(sketch, "H1 @1 Title") {
		t.Fatalf("heading missing from sketch: %q", sketch[:120])
	}
	last := sketchLineFor(t, sketch, "Final chapter opens here")
	if _, ok := lines[last]; !ok {
		t.Fatal("last sketch line missing from lookup map")
	}
}

func TestParseChapterPlans(t *testing.T) {
	entries := []string{"One", "Two"}
	plans, err := parseChapterPlans("```json\n{\"chapters\":[{\"chapter\":1,\"line\":10},{\"chapter\":2,\"line\":20}]}\n```", entries)
	if err != nil || len(plans) != 2 || plans[0].Title != "One" || plans[1].Line != 20 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if _, err := parseChapterPlans(`{"chapters":[{"chapter":9,"line":10},{"chapter":2,"line":20}]}`, entries); err == nil {
		t.Fatal("out-of-range chapter index must fail when fewer than 2 usable remain")
	}
	plans, err = parseChapterPlans(`{"chapters":[{"title":"Alpha","line":5},{"title":"Beta","line":9}]}`, nil)
	if err != nil || plans[1].Title != "Beta" {
		t.Fatalf("freeform plans=%+v err=%v", plans, err)
	}
}
