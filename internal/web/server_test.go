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
	if strings.Contains(string(indexBody), `id="save-note"`) {
		t.Fatal("served reader still contains the old Save button")
	}
	if !strings.Contains(string(indexBody), `src="/membox/integration.js"`) {
		t.Fatalf("index.html missing isolated membox adapter: %s", indexBody[:200])
	}
	adapterResp, err := http.Get(baseURL + "/membox/integration.js")
	if err != nil {
		t.Fatal(err)
	}
	adapterBody, _ := io.ReadAll(adapterResp.Body)
	adapterResp.Body.Close()
	if adapterResp.StatusCode != http.StatusOK || !strings.Contains(string(adapterBody), "membox-connection") {
		t.Fatalf("membox adapter unavailable: status=%d", adapterResp.StatusCode)
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
	if got := docResp.Header.Get("X-Membox-Filename"); got != "flash.md" {
		t.Fatalf("X-Membox-Filename = %q, want flash.md", got)
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

func TestServerAnnotationSidecarPersistsByDocumentUUID(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	// No annotations yet: GET returns 204 No Content.
	emptyResp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	emptyResp.Body.Close()
	if emptyResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 when no annotations, got %d", emptyResp.StatusCode)
	}

	// Persist a sidecar keyed by the document UUID.
	sidecar := `{"format":"miru-annotations","version":2,"annotations":[]}`
	postResp, err := http.Post(baseURL+"/api/doc/"+docID+"/annotations", "application/json", strings.NewReader(sidecar))
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(postResp.Body)
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("save annotations failed: status=%d body=%q", postResp.StatusCode, postBody)
	}

	// The sidecar round-trips so the reader can restore notes on re-open.
	getResp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK || string(getBody) != sidecar {
		t.Fatalf("unexpected sidecar: status=%d body=%q", getResp.StatusCode, getBody)
	}

	// Posting an empty body clears stored annotations.
	clearResp, err := http.Post(baseURL+"/api/doc/"+docID+"/annotations", "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	clearResp.Body.Close()
	againResp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	againResp.Body.Close()
	if againResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 after clearing, got %d", againResp.StatusCode)
	}
}

func TestServerSyncUpdatesMarkdownAndAnnotationsTogether(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	payload := strings.NewReader(`{"id":"` + docID + `","title":"Flash Attention","body":"# Flash Attention\n\nsynced body\n","annotations":{"format":"miru-annotations","version":2,"annotations":[]}}`)
	resp, err := http.Post(baseURL+"/api/sync", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync failed: status=%d body=%q", resp.StatusCode, responseBody)
	}
	var result struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != docID || result.Created {
		t.Fatalf("sync changed identity: %+v", result)
	}
	saved, err := os.ReadFile(filepath.Join(notesDir, "flash.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "synced body") {
		t.Fatalf("Markdown was not updated: %q", saved)
	}
	annotationsResp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	annotations, _ := io.ReadAll(annotationsResp.Body)
	annotationsResp.Body.Close()
	if annotationsResp.StatusCode != http.StatusOK || !strings.Contains(string(annotations), "miru-annotations") {
		t.Fatalf("annotations were not synced: status=%d body=%q", annotationsResp.StatusCode, annotations)
	}
}

func TestServerSyncCreatesNewDocumentWithAnnotations(t *testing.T) {
	baseURL, _, notesDir := startServer(t)

	payload := strings.NewReader(`{"title":"Synced Paste","body":"# Synced Paste\n\nnew from Miru\n","annotations":{"format":"miru-annotations","version":2,"annotations":[]}}`)
	resp, err := http.Post(baseURL+"/api/sync", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync create failed: status=%d body=%q", resp.StatusCode, body)
	}
	var result struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || !result.Created {
		t.Fatalf("new sync did not create a document: %+v", result)
	}
	canonicalNotes, err := filepath.EvalSymlinks(notesDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canonicalNotes, "synced-paste.md"); result.Path != want {
		t.Fatalf("sync path = %q, want %q", result.Path, want)
	}
	annotationResp, err := http.Get(baseURL + "/api/doc/" + result.ID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	annotationBody, _ := io.ReadAll(annotationResp.Body)
	annotationResp.Body.Close()
	if annotationResp.StatusCode != http.StatusOK || !strings.Contains(string(annotationBody), "miru-annotations") {
		t.Fatalf("new note annotations missing: status=%d body=%q", annotationResp.StatusCode, annotationBody)
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

func TestServerIngestCreatesDocumentAndViewURL(t *testing.T) {
	baseURL, _, notesDir := startServer(t)

	payload := strings.NewReader(`{"title":"Clip Me","body":"# Clip Me\n\nhello from extension\n","source_url":"https://example.com/a"}`)
	resp, err := http.Post(baseURL+"/api/ingest", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest failed: status=%d body=%q", resp.StatusCode, body)
	}

	var result struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		ViewURL string `json:"view_url"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid ingest response %q: %v", body, err)
	}
	if result.ID == "" || !result.Created {
		t.Fatalf("unexpected ingest result: %+v", result)
	}
	if result.ViewURL != baseURL+"/?id="+result.ID {
		t.Fatalf("view_url = %q", result.ViewURL)
	}
	canonicalNotes, err := filepath.EvalSymlinks(notesDir)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(canonicalNotes, "clip-me.md")
	if result.Path != wantPath {
		t.Fatalf("path = %q, want %q", result.Path, wantPath)
	}
	saved, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if !strings.Contains(text, "source_url:") || !strings.Contains(text, "hello from extension") {
		t.Fatalf("saved clip unexpected: %q", text)
	}
}

func TestServerIngestRequiresTokenWhenConfigured(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "a.md"), []byte("# A\n"), 0o600); err != nil {
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

	server := web.NewServer(service)
	server.SetToken("secret-token")
	baseURL, err := server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })

	payload := strings.NewReader(`{"title":"T","body":"# T\n\nbody\n"}`)
	resp, err := http.Post(baseURL+"/api/ingest", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/ingest", strings.NewReader(`{"title":"T","body":"# T\n\nbody\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Membox-Token", "secret-token")
	okResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(okResp.Body)
		t.Fatalf("authed ingest failed: %d %s", okResp.StatusCode, body)
	}

	statusResp, err := http.Get(baseURL + "/api/bridge/status")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var status map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["auth_required"] != true {
		t.Fatalf("status = %#v", status)
	}
}
