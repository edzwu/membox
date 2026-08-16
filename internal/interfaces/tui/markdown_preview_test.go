package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestPreviewCmdPrependsStoredSummary(t *testing.T) {
	app := &fakeApp{
		readBody:       []byte("# 标题\n\n正文内容\n"),
		previewSummary: "核心：贪心=max 采样",
	}
	selectID := "note-001"
	var msg tea.Msg
	done := make(chan struct{})
	gen := uint64(7)
	go func() {
		msg = previewCmd(context.Background(), app, selectID, 60, gen)()
		close(done)
	}()
	<-done
	pm, ok := msg.(previewMsg)
	if !ok {
		t.Fatalf("expected previewMsg, got %T", msg)
	}
	if !strings.Contains(pm.content, "摘要：核心：贪心=max 采样") {
		t.Fatalf("summary line missing: %q", pm.content)
	}
	if !strings.Contains(pm.content, "# 标题") {
		t.Fatalf("body missing: %q", pm.content)
	}
}
