package backend

import (
	"strings"
	"testing"
)

// Multi-line excerpts (code blocks) round-trip through note Markdown without
// losing newlines or indentation, and the ref-less matcher binds them to
// existing notes instead of creating duplicates.
func TestExcerptRoundTripBindsMultilineSelections(t *testing.T) {
	browserExact := "c := make(chan int)  // Allocate a channel.\n// Start the sort in a goroutine.\ngo func() {\n    list.Sort()\n    c <- 1\n}()\n<-c"

	// What the backend writes into the note document.
	noteBody := selectionNoteMarkdown("", browserExact, "像 C++ 的 future")

	excerpt, note, _ := parseClipBody(noteBody)
	if note != "像 C++ 的 future" {
		t.Fatalf("note round-trip lost content: %q", note)
	}
	if !strings.Contains(excerpt, "\n    list.Sort()") {
		t.Fatalf("excerpt lost newlines or indentation: %q", excerpt)
	}
	if strings.TrimSpace(excerpt) != browserExact {
		t.Fatalf("excerpt round-trip mismatch:\n got %q\nwant %q", excerpt, browserExact)
	}

	// The matcher compares dense forms, so even legacy flattened excerpts
	// (newlines collapsed to spaces, indentation dropped) still bind.
	legacyFlattened := "c := make(chan int) // Allocate a channel. // Start the sort in a goroutine. go func() { list.Sort() c <- 1 }() <-c"
	if denseExcerpt(legacyFlattened) != denseExcerpt(browserExact) {
		t.Fatalf("dense matcher would not bind legacy flattened excerpt")
	}
	if denseExcerpt(excerpt) != denseExcerpt(browserExact) {
		t.Fatalf("dense matcher would not bind round-tripped excerpt")
	}
}
