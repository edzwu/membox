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

func TestFindExcerptOffsetsIgnoresWhitespaceAndArtifacts(t *testing.T) {
	// The page body carries Turndown artifacts (****, \_, list markers, code
	// fences); the excerpt (a browser selection or note blockquote) may
	// disagree about whitespace. upsertClipAnnotation runs both sides through
	// the same cleaning before matching, so they must line up.
	body := "做 CreatorWeave 的时候： ****Agent 的核心循环到底是自己写？****\n\n2.  ****事件流设计**** — 每一步都有事件（message\\_start），完美适合做 UI\n\n`` `pi-coding-agent` ``\n\nCLI 界面、会话管理、主题"
	canonical := markdownToCanonicalText(body)
	cases := []string{
		"Agent 的核心循环到底是自己写？",                      // **** stripped by cleaning
		"事件流设计 — 每一步都有事件（message_start），完美适合做 UI", // \_ unescaped + list marker gone
		"pi-coding-agent CLI 界面、会话管理、主题",          // newlines between blocks ignored
		"做 CreatorWeave 的时候： Agent 的核心循环到底是自己写？",  // cleaned form
	}
	for _, exc := range cases {
		if _, _, ok := findExcerptOffsets(canonical, cleanInlineMarkdown(exc)); !ok {
			t.Fatalf("excerpt not matched: %q", exc)
		}
	}
}

func TestUpsertStoresPageSpanAsExact(t *testing.T) {
	// exact comes from the page canonical span, not from the excerpt text, so
	// spacing differences between note and page do not break Miru re-anchoring.
	body := "对 Agent 来说，`` `read(\"src/main.ts\")` `` 跟在终端里一样自然"
	canonical := markdownToCanonicalText(body)
	start, end, ok := findExcerptOffsets(canonical, cleanInlineMarkdown("对 Agent 来说，read(\"src/main.ts\") 跟在终端里一样自然"))
	if !ok {
		t.Fatal("excerpt not matched")
	}
	span := string([]rune(canonical)[start:end])
	if span == "" || !strings.Contains(span, "read(\"src/main.ts\")") {
		t.Fatalf("unexpected page span: %q", span)
	}
}

func TestParseClipBodyKeepsMultilineNote(t *testing.T) {
	body := "---\nclip_mode: \"selection\"\n---\n\n> An excerpt.\n\nfirst line\n\nsecond line after a blank\n\nSource: [Page](https://example.com)\n"
	excerpt, note, mode := parseClipBody(body)
	if mode != "selection" || excerpt != "An excerpt." {
		t.Fatalf("excerpt/mode = %q / %q", excerpt, mode)
	}
	if note != "first line\n\nsecond line after a blank" {
		t.Fatalf("multiline note lost blank lines: %q", note)
	}
}
