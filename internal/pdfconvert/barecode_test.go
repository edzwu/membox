package pdfconvert

import (
	"strings"
	"testing"
)

func TestNormalizeBareCodeBlocksWrapsLooseCode(t *testing.T) {
	input := strings.Join([]string{
		"Consider this code using std::atomic:",
		"",
		"std::atomic<int> ai(0); // initialize ai to 0",
		"",
		"ai = 10;",
		"",
		"// atomically set ai to 10",
		"",
		"std::cout << ai;",
		"",
		"++ai;",
		"",
		"During execution of these statements, other threads reading ai may see only values of 0, 10, or 11.",
	}, "\n")
	want := strings.Join([]string{
		"Consider this code using std::atomic:",
		"",
		"```cpp",
		"std::atomic<int> ai(0); // initialize ai to 0",
		"",
		"ai = 10;",
		"",
		"// atomically set ai to 10",
		"",
		"std::cout << ai;",
		"",
		"++ai;",
		"```",
		"",
		"During execution of these statements, other threads reading ai may see only values of 0, 10, or 11.",
	}, "\n")
	if got := normalizeBareCodeBlocks(input); got != want {
		t.Fatalf("mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestNormalizeBareCodeBlocksMergesAdjacentSnippets(t *testing.T) {
	// Two code chunks separated by one blank line merge into one fence; the
	// prose sentence between them ends the block.
	input := "std::mutex m;\n\n// as before\n\nbool flag(false);\n\n// not std::atomic\n\n// detect event\n\n// tell reacting task // (part 1)\n\n// unlock m via g's dtor\n\nAnd here's the reacting task:"
	got := normalizeBareCodeBlocks(input)
	if c := strings.Count(got, "```"); c != 2 {
		t.Fatalf("want exactly one open+close fence pair, got %d fence markers:\n%s", c, got)
	}
	if !strings.Contains(got, "```cpp\nstd::mutex m;\n\n// as before\n\nbool flag(false);\n\n// not std::atomic\n\n// detect event\n\n// tell reacting task // (part 1)\n\n// unlock m via g's dtor\n```") {
		t.Fatalf("merged block missing or malformed:\n%s", got)
	}
}

func TestNormalizeBareCodeBlocksLeavesProseAlone(t *testing.T) {
	prose := []string{
		"volatile is the way we tell compilers that we’re dealing with special memory.",
		"std::packaged_tasks aren’t copyable, so when pt is passed to the std::thread constructor, it must be wrapped.",
		"int x corresponds to, say, the value reported by a temperature sensor.",
		"auto fut = std::async(f); // run f using default launch policy • It’s not possible to predict whether f will run concurrently with t.",
		"Consider this code using std::atomic:",
	}
	for _, line := range prose {
		if isBareCodeLine(line) {
			t.Errorf("prose misjudged as code: %q", line)
		}
	}
	out := normalizeBareCodeBlocks(strings.Join(prose, "\n\n"))
	if strings.Contains(out, "```") {
		t.Fatalf("prose was wrapped in a fence:\n%s", out)
	}
}

func TestNormalizeBareCodeBlocksKeepsExistingFences(t *testing.T) {
	input := strings.Join([]string{
		"```cpp",
		"std::atomic<int> ai(0);",
		"ai = 10;",
		"```",
		"",
		"auto y = x;",
		"y = x;",
	}, "\n")
	got := normalizeBareCodeBlocks(input)
	if c := strings.Count(got, "```"); c != 4 {
		t.Fatalf("want 4 fence markers (2 existing + 1 new pair), got %d:\n%s", c, got)
	}
	// The pre-existing fence content must not be re-wrapped or altered.
	if !strings.Contains(got, "```cpp\nstd::atomic<int> ai(0);\nai = 10;\n```") {
		t.Fatalf("existing fence altered:\n%s", got)
	}
	if !strings.Contains(got, "```cpp\nauto y = x;\ny = x;\n```") {
		t.Fatalf("new fence missing:\n%s", got)
	}
}

func TestNormalizeBareCodeBlocksSkipsHeadingsListsQuotes(t *testing.T) {
	input := strings.Join([]string{
		"# Chapter 4",
		"",
		"- a list item",
		"",
		"> a quote",
		"",
		"1. numbered",
		"",
		"---",
		"",
		"| a | b |",
		"| - | - |",
	}, "\n")
	got := normalizeBareCodeBlocks(input)
	if strings.Contains(got, "```") {
		t.Fatalf("headings/lists/quotes/tables must not be wrapped:\n%s", got)
	}
}

func TestNormalizeBareCodeBlocksWorksThroughPostprocess(t *testing.T) {
	// End-to-end: PostprocessMarkdown runs the pass before chapter splitting;
	// a single-chapter book must come back with the code fenced but otherwise
	// whole.
	input := strings.Join([]string{
		"# Book",
		"",
		"## One",
		"",
		"std::atomic<int> ai(0);",
		"",
		"ai = 10;",
		"",
		"More prose.",
	}, "\n")
	result := PostprocessMarkdown(input, "book.md")
	if !strings.Contains(result.IndexMarkdown, "```cpp\nstd::atomic<int> ai(0);\n\nai = 10;\n```") {
		t.Fatalf("bare code not fenced through PostprocessMarkdown:\n%s", result.IndexMarkdown)
	}
}

// 真实转换产物回归:Effective Modern C++ Item 40 的松散代码(修复前形态)。
func TestNormalizeBareCodeBlocksItem40Fragment(t *testing.T) {
	input := strings.Join([]string{
		"Consider this code using std::atomic:",
		"",
		"std::atomic<int> ai(0); // initialize ai to 0",
		"",
		"ai = 10;",
		"",
		"// atomically set ai to 10",
		"",
		"std::cout << ai;",
		"",
		"// atomically read ai's value",
		"",
		"++ai;",
		"",
		"// atomically increment ai to 11",
		"",
		"During execution of these statements, other threads reading ai may see only values of 0, 10, or 11.",
		"",
		"std::atomic<bool> valAvailable(false);",
		"",
		"auto imptValue = computeImportantValue(); // compute value",
		"",
		"valAvailable = true;",
		"",
		"volatile std::atomic<int> vai; // operations on vai are // atomic and can't be // optimized away",
	}, "\n")

	out := normalizeBareCodeBlocks(input)
	if c := strings.Count(out, "```"); c != 4 { // 2 个块 × 2 标记
		t.Fatalf("want 2 fenced blocks (4 markers), got %d:\n%s", c, out)
	}
	for _, want := range []string{
		"std::atomic<int> ai(0); // initialize ai to 0",
		"ai = 10;",
		"// atomically set ai to 10",
		"std::cout << ai;",
		"++ai;",
		"volatile std::atomic<int> vai;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing code line %q in output:\n%s", want, out)
		}
	}
	prose := "During execution of these statements"
	foundProse := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, prose) {
			foundProse = true
		}
	}
	if !foundProse {
		t.Fatal("prose sentence was not preserved")
	}
}
