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

// TestCreateNoteAfterExternalFileDeletionReusesIndexedPath is a regression
// test: a note whose file was deleted externally (catalog still tracks the
// path) used to fail with "created note ... was not indexed" because the
// rescan reports the path as Updated, not Added. Recreating must succeed and
// reuse the same document identity.
func TestCreateNoteAfterExternalFileDeletionReusesIndexedPath(t *testing.T) {
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
	first, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Bubble Sort"})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Document.ID == "" {
		t.Fatal("first note has no id")
	}
	// External deletion: the file vanishes but the catalog still tracks the
	// relative path (no scan runs in between).
	if err := os.Remove(first.Path); err != nil {
		t.Fatal(err)
	}

	second, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "Bubble Sort"})
	if err != nil {
		t.Fatalf("recreate after external deletion: %v", err)
	}
	if second.Document.ID != first.Document.ID {
		t.Fatalf("expected reused document id %s, got %s", first.Document.ID, second.Document.ID)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("recreated file missing: %v", err)
	}
}
