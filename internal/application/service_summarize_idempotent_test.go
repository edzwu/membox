package application_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func newSummarizeTestService(t *testing.T) *application.Service {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
	if _, err := service.AddPath(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return service
}

// Second trigger must reuse the existing summary note without any model call.
func TestSummarizeDocumentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service := newSummarizeTestService(t)

	source, err := service.CreateNote(ctx, application.CreateNoteOptions{
		Title: "乐观锁",
		Body:  "# 乐观锁\n\n" + "CAS 比较并交换，版本号检查。" ,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a summary note exactly like the pipeline would persist it.
	summaryBody := fmt.Sprintf("---\nkind: \"summary\"\nsource_document_id: \"%s\"\nmodel: \"mmd-local\"\n---\n\n# 摘要 · 乐观锁\n\n已有摘要。\n", source.Document.ID)
	seeded, err := service.CreateNote(ctx, application.CreateNoteOptions{
		Title:        "摘要 · 乐观锁",
		Body:         summaryBody,
		FromSelector: string(source.Document.ID),
	})
	if err != nil {
		t.Fatal(err)
	}

	completerCalled := false
	result, err := service.SummarizeDocument(ctx, string(source.Document.ID), false,
		func(context.Context, string) (string, error) {
			completerCalled = true
			return "", fmt.Errorf("model must not be called when a summary exists")
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completerCalled {
		t.Fatal("completer was called despite an existing summary note")
	}
	if !result.Existing {
		t.Fatalf("expected Existing=true, got %+v", result)
	}
	if result.NoteID != string(seeded.Document.ID) {
		t.Fatalf("reused wrong note: got %s want %s", result.NoteID, seeded.Document.ID)
	}

	// force=true bypasses the reuse path (and here fails inside the completer,
	// proving the model was invoked).
	_, err = service.SummarizeDocument(ctx, string(source.Document.ID), true,
		func(context.Context, string) (string, error) {
			return "", fmt.Errorf("forced run reached the model")
		}, nil)
	if err == nil {
		t.Fatal("force run should have invoked the completer and failed")
	}
}

// A summary note belonging to a DIFFERENT source must not be reused.
func TestSummarizeDocumentIgnoresForeignSummaryNotes(t *testing.T) {
	ctx := context.Background()
	service := newSummarizeTestService(t)

	other, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "别的文档", Body: "# 别的\n\n内容"})
	if err != nil {
		t.Fatal(err)
	}
	foreignBody := fmt.Sprintf("---\nkind: \"summary\"\nsource_document_id: \"%s\"\n---\n\n# 摘要 · 别的文档\n\n别人的摘要。\n", other.Document.ID)
	if _, err := service.CreateNote(ctx, application.CreateNoteOptions{
		Title:        "摘要 · 别的文档",
		Body:         foreignBody,
		FromSelector: string(other.Document.ID),
	}); err != nil {
		t.Fatal(err)
	}

	// No summary for THIS document yet; without a completer the pipeline must
	// reach the model stage (and fail there), proving no idempotent shortcut.
	target, err := service.CreateNote(ctx, application.CreateNoteOptions{Title: "目标文档", Body: "# 目标\n\n内容"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SummarizeDocument(ctx, string(target.Document.ID), false,
		func(context.Context, string) (string, error) {
			return "", fmt.Errorf("reached model")
		}, nil)
	if err == nil {
		t.Fatal("expected the pipeline to reach the completer")
	}
}
