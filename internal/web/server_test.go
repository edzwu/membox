package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"membox/internal/bootstrap"
	"membox/internal/web"
)

// startServer builds a real service backed by a temp database with one indexed
// Markdown file, starts the web server on a free port, and returns the base URL
// plus the indexed document's UUID.
func startServer(t *testing.T) (baseURL, docID, notesDir string) {
	t.Helper()
	ctx := context.Background()

	home := t.TempDir()
	notesDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "flash.md"), []byte("# Flash Attention\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service, err := bootstrap.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	if _, err := service.AddPath(ctx, notesDir); err != nil {
		t.Fatal(err)
	}
	records, err := service.ListDocuments(ctx, 10, false)
	if err != nil || len(records) != 1 {
		t.Fatalf("expected one indexed document, got %d (err=%v)", len(records), err)
	}
	docID = string(records[0].Document.ID)

	server := web.NewServer(service)
	baseURL, err = server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })
	return baseURL, docID, notesDir
}

func TestServerServesReaderAndMarkdown(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	indexResp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	indexBody, _ := io.ReadAll(indexResp.Body)
	indexResp.Body.Close()
	if !strings.Contains(string(indexBody), `id="save-note"`) || !strings.Contains(string(indexBody), "membox-loader.js") {
		t.Fatalf("index.html missing membox integration: %s", indexBody[:200])
	}

	docResp, err := http.Get(baseURL + "/api/doc/" + docID)
	if err != nil {
		t.Fatal(err)
	}
	docBody, _ := io.ReadAll(docResp.Body)
	docResp.Body.Close()
	if docResp.StatusCode != http.StatusOK || !strings.Contains(string(docBody), "# Flash Attention") {
		t.Fatalf("unexpected doc response: status=%d body=%q", docResp.StatusCode, docBody)
	}

	missingResp, err := http.Get(baseURL + "/api/doc/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing doc, got %d", missingResp.StatusCode)
	}
}

func TestServerSaveCreatesDocumentWithUUID(t *testing.T) {
	baseURL, _, notesDir := startServer(t)

	payload := strings.NewReader(`{"title":"Online Softmax","body":"# Online Softmax\n\nsaved from browser\n"}`)
	resp, err := http.Post(baseURL+"/api/save", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save failed: status=%d body=%q", resp.StatusCode, body)
	}

	var result struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid save response %q: %v", body, err)
	}
	if result.ID == "" {
		t.Fatal("save did not assign a UUID")
	}
	canonicalNotes, err := filepath.EvalSymlinks(notesDir)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(canonicalNotes, "online-softmax.md")
	if result.Path != wantPath {
		t.Fatalf("save path = %q, want %q", result.Path, wantPath)
	}
	saved, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("saved file not on disk: %v", err)
	}
	if !strings.Contains(string(saved), "saved from browser") {
		t.Fatalf("saved file content unexpected: %q", saved)
	}
}
