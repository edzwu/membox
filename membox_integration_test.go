package membox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"membox"
)

func TestMVP_PathAddSearchEditRenameAndRestoreIdentity(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	original := filepath.Join(notes, "a.md")
	if err := os.WriteFile(original, []byte("# Alpha\n\nflash attention\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "ignored.txt"), []byte("flash attention"), 0o600); err != nil {
		t.Fatal(err)
	}

	box, err := membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()

	added, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes})
	if err != nil {
		t.Fatal(err)
	}
	if added.Scan.Added != 1 || added.Path.Documents != 1 {
		t.Fatalf("unexpected add result: %+v", added)
	}

	hits, err := box.SearchDocuments(ctx, membox.SearchDocumentsQuery{Query: "flash attention", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d search hits", len(hits))
	}
	documentID := hits[0].DocumentID

	if err := os.WriteFile(original, []byte("# Alpha\n\nupdated online softmax\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := box.ScanPaths(ctx, membox.ScanPathsCommand{}); err != nil {
		t.Fatal(err)
	}
	view, err := box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documentID})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != documentID {
		t.Fatalf("content edit changed identity: %s -> %s", documentID, view.ID)
	}

	renamed := filepath.Join(notes, "renamed.md")
	if err := os.Rename(original, renamed); err != nil {
		t.Fatal(err)
	}
	report, err := box.ScanPaths(ctx, membox.ScanPathsCommand{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Renamed != 1 {
		t.Fatalf("rename was not detected: %+v", report)
	}
	view, err = box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documentID[:12]})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != documentID || filepath.Base(view.Path) != "renamed.md" {
		t.Fatalf("identity/location mismatch: %+v", view)
	}

	if _, err := box.RemovePath(ctx, membox.RemovePathCommand{Selector: "1"}); err != nil {
		t.Fatal(err)
	}
	view, err = box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documentID})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "untracked" {
		t.Fatalf("expected untracked, got %s", view.Status)
	}

	readded, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes})
	if err != nil {
		t.Fatal(err)
	}
	if readded.AlreadyExists {
		t.Fatal("removed path was not reactivated and scanned")
	}
	view, err = box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documentID})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "active" || view.ID != documentID {
		t.Fatalf("re-add did not restore identity: %+v", view)
	}

	body, err := box.ReadDocument(ctx, membox.ReadDocumentQuery{Selector: documentID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "online softmax") {
		t.Fatalf("read wrong content: %q", body)
	}
}

func TestMVP_DoesNotCreateMarkdownCopiesOrBlobs(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(notes, "a.md"), []byte("# A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes}); err != nil {
		t.Fatal(err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(strings.ToLower(entry.Name()), ".md") || entry.Name() == "blobs") {
			t.Fatalf("unexpected content storage %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
