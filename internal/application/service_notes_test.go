package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

// TestCreateNoteDoesNotClobberTrackedPath is a regression test for the
// Effective-Go incident: creating a note whose slug collides with a
// catalog-tracked path must never clobber that document. The note-new flow
// now skips tracked paths (disk AND catalog), and when content differs the
// scan's content-hash rename reconciliation cannot merge them either.
func TestUpsertMarkdownCreatesExactGeneratedFilenameWithoutReslugifying(t *testing.T) {
	ctx := context.Background()
	home, notesDir := t.TempDir(), t.TempDir()
	store, err := sqlite.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
	if _, err := service.AddPath(ctx, notesDir); err != nil {
		t.Fatal(err)
	}

	const filename = "Fooled-by-randomness--pdf-01a001106cb479db952c02e705f8c0ea.md"
	first, err := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: "# Fooled by randomness\n\nbody\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || filepath.Base(first.Path) != filename {
		t.Fatalf("exact generated filename was changed: %+v", first)
	}
	if _, err := os.Stat(filepath.Join(notesDir, "fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea.md")); !os.IsNotExist(err) {
		t.Fatalf("secondary slugified file was created: %v", err)
	}
	second, err := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: "# Fooled by randomness\n\nupdated\n"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Document.ID != first.Document.ID || second.Path != first.Path {
		t.Fatalf("exact generated upsert did not preserve identity: first=%+v second=%+v", first, second)
	}
}

func TestCreateNoteDoesNotClobberTrackedPath(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	notesDir := t.TempDir()

	store, err := sqlite.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})

	if _, err := service.AddPath(ctx, notesDir); err != nil {
		t.Fatal(err)
	}
	// First note at bubble-sort.md.
	first, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Bubble Sort"})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// The tracked file is deleted externally (no scan runs in between).
	if err := os.Remove(first.Path); err != nil {
		t.Fatal(err)
	}

	// Same slug, different content: must land on a fresh path and a fresh
	// identity — never reuse (and thereby clobber) the tracked document.
	second, err := service.CreateNote(ctx, application.CreateNoteOptions{
		Title: "Bubble Sort",
		Body:  "# bubble-sort\n\nOptimized version with early exit and linear best case.\n",
	})
	if err != nil {
		t.Fatalf("recreate after external deletion: %v", err)
	}
	if filepath.Base(second.Path) == filepath.Base(first.Path) {
		t.Fatalf("recreate reused the tracked filename: %s", second.Path)
	}
	if second.Document.ID == first.Document.ID {
		t.Fatalf("recreate merged identities despite different content: %s", second.Document.ID)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("recreated file missing: %v", err)
	}
	// The original document is untouched (still missing on disk, same id).
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("tracked document's file should stay absent: %v", err)
	}
}
