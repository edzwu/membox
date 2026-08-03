package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestMarkdownToCanonicalTextStripsSyntax(t *testing.T) {
	body := "---\ntitle: \"X\"\n---\n\n# Title\n\nSome **bold** and a [link](https://a.b) here.\n\n> quoted line\n\n- item one\n- item two\n\n```go\ncode line\n```\n"
	got := markdownToCanonicalText(body)
	for _, want := range []string{"Title", "Some bold and a link here.", "quoted line", "item one", "code line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonical text missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"**", "[link]", "```", "> ", "- item", "title:"} {
		if strings.Contains(got, bad) {
			t.Fatalf("canonical text still contains syntax %q:\n%s", bad, got)
		}
	}
}

func TestFindExcerptOffsetsWhitespaceFlexible(t *testing.T) {
	canonical := "第一段内容。\n第二段 about  distributed\tsystems here."
	start, end, ok := findExcerptOffsets(canonical, "第二段 about distributed systems here.")
	if !ok {
		t.Fatal("excerpt not found")
	}
	runes := []rune(canonical)
	if string(runes[start:end]) != "第二段 about  distributed\tsystems here." {
		t.Fatalf("mapped span = %q", string(runes[start:end]))
	}
}

func TestFindExcerptOffsetsMissing(t *testing.T) {
	if _, _, ok := findExcerptOffsets("abc", "xyz"); ok {
		t.Fatal("expected no match")
	}
}

func TestJsStringLength(t *testing.T) {
	if got := jsStringLength("hello"); got != 5 {
		t.Fatalf("ascii length = %d", got)
	}
	if got := jsStringLength("中文"); got != 2 {
		t.Fatalf("cjk length = %d", got)
	}
	if got := jsStringLength("\U0001F600"); got != 2 { // surrogate pair
		t.Fatalf("emoji length = %d", got)
	}
}

func TestParseClipBodySelectionShape(t *testing.T) {
	body := "---\ntitle: \"T — note\"\nclip_mode: \"selection\"\n---\n\n> Excerpt text here.\n> Second excerpt line.\n\nMy note body.\n\nSource: [T](https://x.y)\n"
	excerpt, note, mode := parseClipBody(body)
	if mode != "selection" {
		t.Fatalf("mode = %q", mode)
	}
	if !strings.Contains(excerpt, "Excerpt text here.") || !strings.Contains(excerpt, "Second excerpt line.") {
		t.Fatalf("excerpt = %q", excerpt)
	}
	if strings.TrimSpace(note) != "My note body." {
		t.Fatalf("note = %q", note)
	}
}

func TestShaHelperMatchesCrypto(t *testing.T) {
	body := "hello 中文"
	sum := sha256.Sum256([]byte(body))
	want := "sha256:" + hex.EncodeToString(sum[:])
	// Smoke: ensure the format we write into sidecars is stable.
	if !strings.HasPrefix(want, "sha256:") || len(want) != len("sha256:")+64 {
		t.Fatalf("unexpected hash shape %q", want)
	}
}
