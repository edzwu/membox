package application_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func TestExtractFirstMarkdownH1(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"plain", "# Hello World\n\nbody\n", "Hello World"},
		{"skips front matter", "---\ntitle: x\n---\n\n# Real Title\n", "Real Title"},
		{"skips h2", "## Not\n\n# Yes\n", "Yes"},
		{"skips fence", "```\n# fake\n```\n# Outside\n", "Outside"},
		{"none", "just prose\n\n- list\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := application.ExtractFirstMarkdownH1(tc.body); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

type stubNamer struct {
	title string
	err   error
	calls int
}

func (s *stubNamer) NameNote(context.Context, string) (string, error) {
	s.calls++
	return s.title, s.err
}

func TestFinalizeQuickNoteFromH1(t *testing.T) {
	service, notesDir := newQuickNoteService(t)
	ctx := context.Background()

	created, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Untitled", Body: "\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SyncDocument(ctx, string(created.Document.ID), "# Latency Numbers\n\nL1 cache is fast.\n"); err != nil {
		t.Fatal(err)
	}

	namer := &stubNamer{title: "should-not-be-used"}
	result, err := service.FinalizeQuickNote(ctx, string(created.Document.ID), namer)
	if err != nil {
		t.Fatal(err)
	}
	if namer.calls != 0 {
		t.Fatalf("LLM namer called %d times; H1 should win", namer.calls)
	}
	if result.Filename != "latency-numbers.md" {
		t.Fatalf("filename=%q want latency-numbers.md (notesDir=%s)", result.Filename, notesDir)
	}
	if result.Title != "Latency Numbers" {
		t.Fatalf("title=%q", result.Title)
	}
	if result.UsedLLM || result.Deleted {
		t.Fatalf("unexpected flags: %+v", result)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeQuickNoteUsesLLMWithoutH1(t *testing.T) {
	service, _ := newQuickNoteService(t)
	ctx := context.Background()

	created, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Untitled", Body: "\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SyncDocument(ctx, string(created.Document.ID), "prefill is compute-bound; decode is memory-bound.\n"); err != nil {
		t.Fatal(err)
	}

	namer := &stubNamer{title: "Prefill vs Decode"}
	result, err := service.FinalizeQuickNote(ctx, string(created.Document.ID), namer)
	if err != nil {
		t.Fatal(err)
	}
	if namer.calls != 1 {
		t.Fatalf("namer calls=%d", namer.calls)
	}
	if !result.UsedLLM {
		t.Fatal("expected UsedLLM")
	}
	if result.Filename != "prefill-vs-decode.md" {
		t.Fatalf("filename=%q", result.Filename)
	}
}

func TestFinalizeQuickNoteTrashesEmpty(t *testing.T) {
	service, _ := newQuickNoteService(t)
	ctx := context.Background()

	created, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Untitled", Body: "\n"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.FinalizeQuickNote(ctx, string(created.Document.ID), &stubNamer{title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Deleted {
		t.Fatalf("expected deleted, got %+v", result)
	}
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("scratch file should be gone: err=%v", err)
	}
}

func TestQuickNoteTitlePrompt(t *testing.T) {
	prompt := application.QuickNoteTitlePrompt("body text here")
	if !strings.Contains(prompt, "body text here") {
		t.Fatalf("prompt missing body: %s", prompt)
	}
}

func newQuickNoteService(t *testing.T) (*application.Service, string) {
	t.Helper()
	home, notesDir := t.TempDir(), t.TempDir()
	store, err := sqlite.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
	if _, err := service.AddPath(context.Background(), notesDir); err != nil {
		t.Fatal(err)
	}
	return service, notesDir
}
