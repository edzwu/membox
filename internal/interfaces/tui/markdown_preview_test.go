package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownPreviewWrapsAndStylesHeadings(t *testing.T) {
	src := "---\ntitle: x\n---\n\n# Hello\n\nThis is a long paragraph that should wrap when the preview width is narrow enough for word wrap to kick in for terminal reading of notes.\n"
	out := renderMarkdownPreview(src, 40)
	if strings.Contains(out, "title: x") {
		t.Fatalf("front matter leaked into preview:\n%s", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("heading missing from render:\n%s", out)
	}
	if !strings.Contains(out, "\n") {
		t.Fatalf("expected wrapped multi-line output, got single line %q", out)
	}
}

func TestRenderMarkdownPreviewPlainPlaceholder(t *testing.T) {
	out := renderMarkdownPreview("PDF document\n\n/tmp/a.pdf\n", 30)
	if !strings.Contains(out, "PDF document") {
		t.Fatalf("placeholder lost: %q", out)
	}
}
