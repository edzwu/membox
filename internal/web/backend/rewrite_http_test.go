package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"membox/internal/application"
	"membox/internal/docrewrite"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func newRewriteTestServer(t *testing.T, notesDir string) *Server {
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
	server := NewServer(service, fstest.MapFS{}, fstest.MapFS{})
	server.SetHome(t.TempDir())
	return server
}

func rewriteSelector(t *testing.T, server *Server) string {
	t.Helper()
	docs, err := server.service.ListDocuments(context.Background(), 10, false, "")
	if err != nil || len(docs) != 1 {
		t.Fatalf("expected 1 scanned document, got %d, err=%v", len(docs), err)
	}
	return string(docs[0].Document.ID)
}

func TestDocumentRewriteStreamsPreviewAndNeverWrites(t *testing.T) {
	notesDir := t.TempDir()
	notePath := filepath.Join(notesDir, "messy.md")
	original := "# Messy\n\n### Orphan heading\n\nbody\n"
	if err := os.WriteFile(notePath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newRewriteTestServer(t, notesDir)

	var captured struct {
		title string
		body  string
		model string
		hint  string
	}
	restore := runDocumentRewrite
	runDocumentRewrite = func(_ context.Context, _ string, title, markdown, model, hint string, onProgress func(docrewrite.Progress)) (docrewrite.Result, error) {
		captured.title, captured.body, captured.model, captured.hint = title, markdown, model, hint
		if onProgress != nil {
			onProgress(docrewrite.Progress{Done: 1, Total: 1, Detail: "chunk 1/1"})
		}
		return docrewrite.Result{
			Markdown: "# Messy\n\n## Orphan heading\n\nbody\n",
			Label:    "local qwen3:14b",
			Provider: "ollama",
			Model:    "qwen3:14b",
			Chunks:   1,
		}, nil
	}
	defer func() { runDocumentRewrite = restore }()

	// The Miru header guards the endpoint against cross-origin pages.
	blocked := httptest.NewRecorder()
	selector := rewriteSelector(t, server)
	server.handleDocumentRewrite(blocked, httptest.NewRequest(http.MethodPost, "/api/doc/x/rewrite", nil), selector)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("without Miru header status=%d", blocked.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/doc/messy/rewrite", strings.NewReader(`{"model":"local"}`))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleDocumentRewrite(response, request, selector)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, marker := range []string{`"type":"progress"`, `"type":"done"`, `## Orphan heading`, `"label":"local qwen3:14b"`} {
		if !strings.Contains(body, marker) {
			t.Fatalf("stream missing %s: %q", marker, body)
		}
	}

	// The default hint constrains the model to layout-only work.
	if !strings.Contains(captured.hint, "逐字保留") || !strings.Contains(captured.hint, "标题层级") {
		t.Fatalf("default polish hint missing: %q", captured.hint)
	}
	if captured.model != "local" || captured.body != original || captured.title != "Messy" {
		t.Fatalf("rewrite input mismatch: %+v", captured)
	}

	// Preview semantics: the source file must be untouched; saving is the
	// confirm flow's job via /api/sync.
	after, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("rewrite endpoint must not write the file, got %q", string(after))
	}
}

func TestDocumentRewriteRejectsBadInput(t *testing.T) {
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "note.md"), []byte("# Note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newRewriteTestServer(t, notesDir)
	selector := rewriteSelector(t, server)

	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doc/note/rewrite", nil)
	req.Header.Set(miruClientHeader, "1")
	server.handleDocumentRewrite(get, req, selector)
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d", get.Code)
	}

	missing := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/doc/nope/rewrite", nil)
	req.Header.Set(miruClientHeader, "1")
	server.handleDocumentRewrite(missing, req, "nope")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing document status=%d body=%q", missing.Code, missing.Body.String())
	}
}

// A model failure after streaming started must arrive as an NDJSON error
// event, not an HTTP status the fetch reader cannot surface mid-stream.
func TestDocumentRewriteStreamsMidRunErrors(t *testing.T) {
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "note.md"), []byte("# Note\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newRewriteTestServer(t, notesDir)

	restore := runDocumentRewrite
	runDocumentRewrite = func(_ context.Context, _ string, _, _, _, _ string, onProgress func(docrewrite.Progress)) (docrewrite.Result, error) {
		if onProgress != nil {
			onProgress(docrewrite.Progress{Done: 0, Total: 2, Detail: "chunk 1/2"})
		}
		return docrewrite.Result{}, context.DeadlineExceeded
	}
	defer func() { runDocumentRewrite = restore }()

	request := httptest.NewRequest(http.MethodPost, "/api/doc/note/rewrite", nil)
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleDocumentRewrite(response, request, rewriteSelector(t, server))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var sawProgress, sawError bool
	for _, line := range strings.Split(strings.TrimSpace(response.Body.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		sawProgress = sawProgress || event["type"] == "progress"
		sawError = sawError || event["type"] == "error"
	}
	if !sawProgress || !sawError {
		t.Fatalf("expected progress+error events, got %q", response.Body.String())
	}
}
