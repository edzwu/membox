package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func newTrashTestServer(t *testing.T, notesDir string) *Server {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
	if _, err := service.AddPath(context.Background(), notesDir); err != nil {
		t.Fatal(err)
	}
	return NewServer(service, fstest.MapFS{}, fstest.MapFS{})
}

func TestDocumentTrashSoftDeletesAndRequiresMiruHeader(t *testing.T) {
	notesDir := t.TempDir()
	notePath := filepath.Join(notesDir, "doomed-note.md")
	if err := os.WriteFile(notePath, []byte("# Doomed\n\nbye\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newTrashTestServer(t, notesDir)

	docs, err := server.service.ListDocuments(context.Background(), 10, false, "")
	if err != nil || len(docs) != 1 {
		t.Fatalf("expected 1 scanned document, got %d, err=%v", len(docs), err)
	}
	selector := string(docs[0].Document.ID) // same identity the reader's ?id= carries

	// Without the Miru header the action must not run.
	blocked := httptest.NewRecorder()
	server.handleDocumentTrash(blocked, httptest.NewRequest(http.MethodPost, "/api/doc/x/trash", nil), selector)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("without Miru header status=%d", blocked.Code)
	}
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("file must survive a rejected trash request: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/doc/x/trash", nil)
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleDocumentTrash(response, request, selector)
	if response.Code != http.StatusOK {
		t.Fatalf("trash status=%d body=%q", response.Code, response.Body.String())
	}
	var payload struct {
		OK        bool   `json:"ok"`
		TrashedTo string `json:"trashed_to"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.TrashedTo == "" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if _, err := os.Stat(notePath); !os.IsNotExist(err) {
		t.Fatalf("original file should be moved away, stat err=%v", err)
	}
	if _, err := os.Stat(payload.TrashedTo); err != nil {
		t.Fatalf("trashed copy should exist at %s: %v", payload.TrashedTo, err)
	}

	// Trashing twice reports an error, not a second move.
	again := httptest.NewRecorder()
	second := httptest.NewRequest(http.MethodPost, "/api/doc/x/trash", nil)
	second.Header.Set(miruClientHeader, "1")
	server.handleDocumentTrash(again, second, selector)
	if again.Code == http.StatusOK {
		t.Fatalf("double trash must not succeed: %q", again.Body.String())
	}
}
