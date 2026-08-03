package membox_test

import (
	"context"
	"io"
	"net/http"
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

func TestDocumentDeleteFileRemovesFileAndMarksMissingAfterScan(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	path := filepath.Join(notes, "delete.md")
	if err := os.WriteFile(path, []byte("# Delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	if _, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes}); err != nil {
		t.Fatal(err)
	}
	documents, err := box.ListDocuments(ctx, membox.ListDocumentsQuery{Limit: 10})
	if err != nil || len(documents) != 1 {
		t.Fatalf("list failed: count=%d err=%v", len(documents), err)
	}
	result, err := box.DeleteDocument(ctx, membox.DeleteDocumentCommand{Selector: documents[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.DocumentID != documents[0].ID || result.Path != documents[0].Path {
		t.Fatalf("unexpected delete result: %+v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	document, err := box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documents[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if document.Status != "missing" {
		t.Fatalf("deleted document status=%s", document.Status)
	}
}

func TestDocumentPinPersistsAcrossReopenAndTogglesOff(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(notes, "pinned.md"), []byte("# Pinned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes}); err != nil {
		t.Fatal(err)
	}
	documents, err := box.ListDocuments(ctx, membox.ListDocumentsQuery{Limit: 10})
	if err != nil || len(documents) != 1 {
		t.Fatalf("listing document: count=%d err=%v", len(documents), err)
	}
	result, err := box.ToggleDocumentPin(ctx, membox.ToggleDocumentPinCommand{Selector: documents[0].ID})
	if err != nil || !result.Pinned {
		t.Fatalf("pinning document: result=%+v err=%v", result, err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}

	box, err = membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	document, err := box.GetDocument(ctx, membox.GetDocumentQuery{Selector: documents[0].ID})
	if err != nil || !document.Pinned {
		t.Fatalf("pin did not persist: document=%+v err=%v", document, err)
	}
	result, err = box.ToggleDocumentPin(ctx, membox.ToggleDocumentPinCommand{Selector: document.ID})
	if err != nil || result.Pinned {
		t.Fatalf("unpinning document: result=%+v err=%v", result, err)
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

func TestScanAutoProjectsExistingClipNotes(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()

	page := `---
title: "Legacy Page"
source_url: "https://example.com/legacy"
clipper: membox-clipper
clip_mode: "page"
---

# Legacy Page

A very distinctive sentence that predates the projection feature.
`
	note := `---
title: "A very distinctive sentence… — note"
source_url: "https://example.com/legacy"
clipper: membox-clipper
clip_mode: "selection"
---

> A very distinctive sentence that predates the projection feature.

Legacy margin note.

Source: [Legacy Page](https://example.com/legacy)
`
	if err := os.WriteFile(filepath.Join(notes, "legacy-page.md"), []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "a-very-distinctive-sentence-note.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}

	box, err := membox.Open(membox.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()

	if _, err := box.AddPath(ctx, membox.AddPathCommand{Directory: notes}); err != nil {
		t.Fatal(err)
	}
	// Scan triggers the auto-repair projection pass (same path as Ctrl+R).
	if _, err := box.ScanPaths(ctx, membox.ScanPathsCommand{}); err != nil {
		t.Fatal(err)
	}
	pages, projected, err := box.ProjectClipAnnotations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 || projected != 1 {
		t.Fatalf("projection stats = %d pages / %d projected, want 1/1", pages, projected)
	}

	server := box.WebServer()
	baseURL, err := server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Shutdown(ctx) }()

	documents, err := box.ListDocuments(ctx, membox.ListDocumentsQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	pageID := ""
	for _, doc := range documents {
		if strings.Contains(doc.Path, "legacy-page.md") {
			pageID = doc.ID
		}
	}
	if pageID == "" {
		t.Fatal("page clip document not found")
	}

	resp, err := http.Get(baseURL + "/api/doc/" + pageID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("annotations status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, "Legacy margin note.") {
		t.Fatalf("projection missing note text: %s", text)
	}
	if !strings.Contains(text, "predates the projection feature") {
		t.Fatalf("projection missing excerpt: %s", text)
	}
	if !strings.Contains(text, `"ref"`) {
		t.Fatalf("projection missing ref join key: %s", text)
	}

	// Idempotent: scanning again must not duplicate the annotation.
	if _, err := box.ScanPaths(ctx, membox.ScanPathsCommand{}); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(baseURL + "/api/doc/" + pageID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if got := strings.Count(string(body2), `"exact"`); got != 1 {
		t.Fatalf("expected exactly 1 annotation after re-scan, got %d", got)
	}
}
