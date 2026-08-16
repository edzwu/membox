package pdfconvert

import (
	"strings"
	"testing"
)

func TestRewriteRelativeMarkdownLinksUsesDocumentIDs(t *testing.T) {
	md := "# Book\n\n## 目录\n\n1. [Intro](book-pdf-abc-chapter-001.md)\n2. [Out](https://example.com/a.md)\n"
	out := rewriteRelativeMarkdownLinks(md, map[string]string{
		"book-pdf-abc-chapter-001.md": "01a00a54-8891-7b90-b27a-07928b81c735",
	})
	if want := "](/?id=01a00a54-8891-7b90-b27a-07928b81c735)"; !strings.Contains(out, want) {
		t.Fatalf("missing id link:\n%s", out)
	}
	if strings.Contains(out, "](book-pdf-abc-chapter-001.md)") {
		t.Fatalf("filename link not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "](https://example.com/a.md)") {
		t.Fatalf("external link was rewritten:\n%s", out)
	}
}

func TestRewriteRelativeMarkdownLinksChapterBacklink(t *testing.T) {
	md := "# Ch\n\n[← Book](book-pdf-abc.md)\n\nbody\n"
	out := rewriteRelativeMarkdownLinks(md, map[string]string{
		"book-pdf-abc.md": "01a00a56-02fc-7cd5-8e82-9f1ad80a54b0",
	})
	if !strings.Contains(out, "](/?id=01a00a56-02fc-7cd5-8e82-9f1ad80a54b0)") {
		t.Fatalf("backlink not rewritten:\n%s", out)
	}
}
