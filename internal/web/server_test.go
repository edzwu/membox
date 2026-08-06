package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"membox/internal/application"
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
	if strings.Contains(string(indexBody), "brand-social") || !strings.Contains(string(indexBody), `class="topbar-center"`) {
		t.Fatal("host-neutral topbar should expose an empty center slot without personal links")
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
	if !strings.Contains(string(adapterBody), "membox-document-switcher") || !strings.Contains(string(adapterBody), "Switch document · Ctrl+O") {
		t.Fatal("membox adapter does not inject the document switcher")
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

func TestServerEncodesUnicodeFilenameHeader(t *testing.T) {
	baseURL, _, _ := startServer(t)
	body := postJSON(t, baseURL+"/api/save", map[string]string{
		"title": "C 实现 Barrier 同步原语",
		"body":  "# C 实现 Barrier 同步原语\n",
	})
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &saved); err != nil || saved.ID == "" {
		t.Fatalf("invalid save response: %q", body)
	}

	resp, err := http.Get(baseURL + "/api/doc/" + saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	const filename = "c-实现-barrier-同步原语.md"
	encoded := resp.Header.Get("X-Membox-Filename")
	if encoded != url.PathEscape(filename) {
		t.Fatalf("encoded filename header = %q, want %q", encoded, url.PathEscape(filename))
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil || decoded != filename {
		t.Fatalf("decoded filename = %q (err=%v), want %q", decoded, err, filename)
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

func TestMiruAnnotationsMaterializeAsMarkdownNoteDocuments(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)
	payload := map[string]any{
		"format": "miru-annotations", "version": 2,
		"replaceAnnotations": true,
		"annotations": []map[string]any{{
			"start": 16, "exact": "body", "prefix": "Flash Attention ", "suffix": "",
			"highlight": true, "underline": false, "strikethrough": false,
			"note": "Remember this passage.", "clientId": "miru-client-1",
		}},
		"progress": map[string]any{"y": 12, "at": "2026-08-04T00:00:00Z"},
	}
	firstSave := postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", payload)
	if !strings.Contains(string(firstSave), `"clientId":"miru-client-1"`) || !strings.Contains(string(firstSave), `"ref":`) {
		t.Fatalf("annotation save did not echo stable client mapping: %q", firstSave)
	}
	// A second save before reload still has no client-side ref. It must update
	// the same note rather than creating a duplicate file.
	postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", payload)

	files, err := filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("annotation note files=%v err=%v", files, err)
	}
	noteBody, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(noteBody), "> body") || !strings.Contains(string(noteBody), "Remember this passage.") {
		t.Fatalf("unexpected annotation Markdown: %q", noteBody)
	}

	resp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var live struct {
		Annotations []struct {
			Exact string `json:"exact"`
			Note  string `json:"note"`
			Ref   string `json:"ref"`
		} `json:"annotations"`
		Progress struct {
			Y int `json:"y"`
		} `json:"progress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&live); err != nil {
		t.Fatal(err)
	}
	if len(live.Annotations) != 1 || live.Annotations[0].Ref == "" || live.Annotations[0].Note != "Remember this passage." || live.Progress.Y != 12 {
		t.Fatalf("live annotation view=%+v", live)
	}

	// Markdown is the note-content authority: an external edit is visible on
	// the next read without rebuilding a page-side projection.
	if err := os.WriteFile(files[0], []byte("> body\n\nEdited outside Miru.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	var edited struct {
		Annotations []struct {
			Note string `json:"note"`
		} `json:"annotations"`
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&edited); err != nil {
		resp2.Body.Close()
		t.Fatal(err)
	}
	resp2.Body.Close()
	if len(edited.Annotations) != 1 || edited.Annotations[0].Note != "Edited outside Miru." {
		t.Fatalf("external Markdown edit not reflected: %+v", edited)
	}
	if edited.Revision <= 0 {
		t.Fatalf("GET annotations missing revision: %+v", edited)
	}

	postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", map[string]any{
		"format": "miru-annotations", "version": 2, "replaceAnnotations": true, "revision": edited.Revision,
		"annotations": []any{}, "progress": map[string]any{"y": 13, "at": "2026-08-04T00:01:00Z"},
	})
	files, err = filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 0 {
		t.Fatalf("deleted annotation note files=%v err=%v", files, err)
	}
}

func TestServerSyncUpdatesMarkdownAndAnnotationsTogether(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	payload := strings.NewReader(`{"id":"` + docID + `","title":"Flash Attention","body":"# Flash Attention\n\nsynced body\n","annotations":{"format":"miru-annotations","version":2,"annotations":[{"start":16,"exact":"synced body","highlight":true,"note":null,"clientId":"sync-client-1"}]}}`)
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
		ID          string `json:"id"`
		Created     bool   `json:"created"`
		Annotations []struct {
			ClientID string `json:"clientId"`
			Ref      string `json:"ref"`
		} `json:"annotations"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != docID || result.Created {
		t.Fatalf("sync changed identity: %+v", result)
	}
	if len(result.Annotations) != 1 || result.Annotations[0].ClientID != "sync-client-1" || result.Annotations[0].Ref == "" {
		t.Fatalf("sync response missing annotation client mapping: %+v", result)
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

func TestIngestSelectionProjectsAnnotationToPage(t *testing.T) {
	baseURL, _, _ := startServer(t)

	pageBody := `---
title: "Deep Article"
source_url: "https://example.com/deep"
clipper: membox-clipper
clip_mode: "page"
---

# Deep Article

Some unique paragraph about distributed consensus and raft leaders.

Another paragraph with more detail about failover behavior.
`
	resp := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Deep Article", "body": pageBody,
		"source_url": "https://example.com/deep", "clip_mode": "page",
	})
	var page testIngestResp
	mustUnmarshal(t, resp, &page)
	if page.ID == "" {
		t.Fatal("page clip not created")
	}

	selBody := `---
title: "Some unique paragraph… — note"
source_url: "https://example.com/deep"
clipper: membox-clipper
clip_mode: "selection"
---

> Some unique paragraph about distributed consensus and raft leaders.

My margin note.

Source: [Deep Article](https://example.com/deep)
`
	resp = postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Some unique paragraph… — note", "body": selBody,
		"source_url": "https://example.com/deep", "clip_mode": "selection",
		"excerpt_raw": "Some unique paragraph about distributed consensus and raft leaders.",
	})
	var note testIngestResp
	mustUnmarshal(t, resp, &note)
	if note.Linked != page.ID {
		t.Fatalf("selection not linked to page: linked=%q page=%q", note.Linked, page.ID)
	}

	annResp, err := http.Get(baseURL + "/api/doc/" + page.ID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer annResp.Body.Close()
	if annResp.StatusCode != http.StatusOK {
		t.Fatalf("annotations status = %d", annResp.StatusCode)
	}
	body, _ := io.ReadAll(annResp.Body)
	assertAnnotationSidecar(t, body, note.ID, "My margin note.", "distributed consensus")
}

func TestIngestPageBackfillsAnnotationsFromEarlierSelections(t *testing.T) {
	baseURL, _, _ := startServer(t)

	selBody := `---
title: "Backfill excerpt… — note"
source_url: "https://example.com/late-page"
clipper: membox-clipper
clip_mode: "selection"
---

> The late page contains this very distinctive sentence.

Early bird note.

Source: [Late](https://example.com/late-page)
`
	resp := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Backfill excerpt… — note", "body": selBody,
		"source_url": "https://example.com/late-page", "clip_mode": "selection",
		"excerpt_raw": "The late page contains this very distinctive sentence.",
	})
	var note testIngestResp
	mustUnmarshal(t, resp, &note)

	pageBody := `---
title: "Late Page"
source_url: "https://example.com/late-page"
clipper: membox-clipper
clip_mode: "page"
---

# Late Page

The late page contains this very distinctive sentence.
`
	resp = postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Late Page", "body": pageBody,
		"source_url": "https://example.com/late-page", "clip_mode": "page",
	})
	var page testIngestResp
	mustUnmarshal(t, resp, &page)

	annResp, err := http.Get(baseURL + "/api/doc/" + page.ID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer annResp.Body.Close()
	if annResp.StatusCode != http.StatusOK {
		t.Fatalf("annotations status = %d", annResp.StatusCode)
	}
	body, _ := io.ReadAll(annResp.Body)
	assertAnnotationSidecar(t, body, note.ID, "Early bird note.", "very distinctive")
}

func postJSON(t *testing.T, url string, payload any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s failed: %d %s", url, resp.StatusCode, body)
	}
	return body
}

func mustUnmarshal(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("invalid response %q: %v", body, err)
	}
}

func assertAnnotationSidecar(t *testing.T, body []byte, noteID, wantNote, wantExcerptPart string) {
	t.Helper()
	var sidecar struct {
		Format       string `json:"format"`
		Version      int    `json:"version"`
		SourceHash   string `json:"sourceHash"`
		SourceLength int    `json:"sourceLength"`
		Annotations  []struct {
			Start     int    `json:"start"`
			End       int    `json:"end"`
			Exact     string `json:"exact"`
			Note      string `json:"note"`
			Ref       string `json:"ref"`
			Highlight bool   `json:"highlight"`
		} `json:"annotations"`
	}
	if err := json.Unmarshal(body, &sidecar); err != nil {
		t.Fatalf("invalid sidecar: %v", err)
	}
	if sidecar.Format != "miru-annotations" || sidecar.Version != 2 {
		t.Fatalf("sidecar header = %q v%d", sidecar.Format, sidecar.Version)
	}
	if !strings.HasPrefix(sidecar.SourceHash, "sha256:") || sidecar.SourceLength <= 0 {
		t.Fatalf("missing source verification fields: %q %d", sidecar.SourceHash, sidecar.SourceLength)
	}
	if len(sidecar.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(sidecar.Annotations))
	}
	ann := sidecar.Annotations[0]
	if !strings.Contains(ann.Exact, wantExcerptPart) {
		t.Fatalf("excerpt = %q", ann.Exact)
	}
	if ann.Note != wantNote {
		t.Fatalf("note = %q, want %q", ann.Note, wantNote)
	}
	if ann.Ref != noteID {
		t.Fatalf("ref = %q, want note id %q", ann.Ref, noteID)
	}
	if !ann.Highlight {
		t.Fatal("annotation should be highlighted")
	}
}

type testIngestResp struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
	ViewURL string `json:"view_url"`
	Linked  string `json:"linked"`
}

func TestSelectionNotesWithSameSlugPrefixUseContentHash(t *testing.T) {
	baseURL, _, notesDir := startServer(t)

	page := `---
title: "Hash Page"
source_url: "https://example.com/hash"
clipper: membox-clipper
clip_mode: "page"
---

# Hash Page

Same prefix unique body one.

Same prefix unique body two.
`
	postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Hash Page", "body": page,
		"source_url": "https://example.com/hash", "clip_mode": "page",
	})

	// Two selection notes whose titles produce the same slug must not collide
	// into a "-2" suffix; the content hash disambiguates them.
	for _, excerpt := range []string{"Same prefix unique body one.", "Same prefix unique body two."} {
		sel := "---\ntitle: \"Same prefix… — note\"\nsource_url: \"https://example.com/hash\"\nclipper: membox-clipper\nclip_mode: \"selection\"\n---\n\n> " + excerpt + "\n\nNote about: " + excerpt + "\n"
		postJSON(t, baseURL+"/api/ingest", map[string]any{
			"title": "Same prefix… — note", "body": sel,
			"source_url": "https://example.com/hash", "clip_mode": "selection",
			"excerpt_raw": excerpt,
		})
	}

	files, err := filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 2 {
		t.Fatalf("expected 2 selection notes, got %v (err=%v)", files, err)
	}
	for _, file := range files {
		base := filepath.Base(file)
		// The exact slug-hash-note shape rules out a "-2" collision suffix
		// (which would appear as ...-note-2.md).
		if !regexp.MustCompile(`^same-prefix-[a-z]{10}-note\.md$`).MatchString(base) {
			t.Fatalf("unexpected selection note filename: %s", base)
		}
		if strings.HasSuffix(base, "-2-note.md") {
			t.Fatalf("collision suffix used: %s", base)
		}
	}
}

func TestAnnotationNoteMarksTargetDocumentModified(t *testing.T) {
	ctx := context.Background()
	home, notesDir := t.TempDir(), t.TempDir()
	page := filepath.Join(notesDir, "page.md")
	if err := os.WriteFile(page, []byte("# Page\n\nA distinctive anchored sentence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Backdate the page file: after note activity its effective modified time
	// must come from the note, not the stale file mtime.
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(page, old, old); err != nil {
		t.Fatal(err)
	}

	service, err := bootstrap.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	if _, err := service.AddPath(ctx, notesDir); err != nil {
		t.Fatal(err)
	}
	records, err := service.ListDocuments(ctx, 10, false)
	if err != nil || len(records) != 1 {
		t.Fatalf("expected 1 document, got %d (err=%v)", len(records), err)
	}
	pageID := records[0].Document.ID
	if since := time.Since(records[0].Document.Index.SourceUpdatedAt); since < 24*time.Hour {
		t.Fatalf("page should look old before any note exists: updated %v ago", since)
	}

	note, err := service.CreateNote(ctx, application.CreateNoteOptions{
		Title:    "A distinctive… — note",
		Body:     "> A distinctive anchored sentence.\n\nMargin note.\n",
		ClipMode: "selection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveAnnotationNote(ctx, application.SaveAnnotationNoteOptions{
		TargetSelector: string(pageID),
		NoteSelector:   string(note.Document.ID),
		Start:          0,
		Highlight:      true,
	}); err != nil {
		t.Fatal(err)
	}

	records, err = service.ListDocuments(ctx, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Document.ID != pageID {
			continue
		}
		if since := time.Since(record.Document.Index.SourceUpdatedAt); since > time.Hour {
			t.Fatalf("annotation note did not mark the target modified: updated %v ago", since)
		}
		return
	}
	t.Fatal("page document missing after note creation")
}

func TestServerRelatedCreatesLinkedPlainDocument(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	payload := `{"title":"Related Idea","body":"# Related Idea\n\nSome related content."}`
	resp, err := http.Post(baseURL+"/api/doc/"+docID+"/related", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create related failed: status=%d body=%q", resp.StatusCode, body)
	}
	var created struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("unexpected create response: %q", body)
	}
	// A plain Markdown document, not a selection note.
	base := filepath.Base(created.Path)
	if strings.HasSuffix(base, "-note.md") {
		t.Fatalf("related document uses note naming: %s", base)
	}
	if _, err := os.Stat(filepath.Join(notesDir, base)); err != nil {
		t.Fatalf("related file missing: %v", err)
	}

	// The source document lists it as an incoming backlink.
	listResp, err := http.Get(baseURL + "/api/doc/" + docID + "/related")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	var listed struct {
		Related []struct {
			ID        string `json:"id"`
			Direction string `json:"direction"`
		} `json:"related"`
	}
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("invalid related list: %q", listBody)
	}
	found := false
	for _, item := range listed.Related {
		if item.ID == created.ID {
			found = true
			if item.Direction != "in" {
				t.Fatalf("related direction = %q, want in", item.Direction)
			}
		}
	}
	if !found {
		t.Fatalf("created document missing from related list: %q", listBody)
	}

	// And the new document sees the source as an outgoing link.
	backResp, err := http.Get(baseURL + "/api/doc/" + created.ID + "/related")
	if err != nil {
		t.Fatal(err)
	}
	backBody, _ := io.ReadAll(backResp.Body)
	backResp.Body.Close()
	if !strings.Contains(string(backBody), `"`+docID+`"`) || !strings.Contains(string(backBody), `"out"`) {
		t.Fatalf("backlink missing from the new document's related list: %q", backBody)
	}
}

func TestServerRelatedSearchesAndLinksExistingDocument(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	saveResp, err := http.Post(baseURL+"/api/save", "application/json", strings.NewReader(`{"title":"Online Softmax","body":"# Online Softmax\n\nexisting card\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	saveBody, _ := io.ReadAll(saveResp.Body)
	saveResp.Body.Close()
	var target struct {
		ID string `json:"id"`
	}
	if saveResp.StatusCode != http.StatusOK || json.Unmarshal(saveBody, &target) != nil || target.ID == "" {
		t.Fatalf("could not create target document: status=%d body=%q", saveResp.StatusCode, saveBody)
	}

	assertCandidate := func(query string, want bool) {
		t.Helper()
		resp, getErr := http.Get(baseURL + "/api/doc/" + docID + "/related/candidates?q=" + query)
		if getErr != nil {
			t.Fatal(getErr)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("candidate search failed: status=%d body=%q", resp.StatusCode, body)
		}
		got := strings.Contains(string(body), `"id":"`+target.ID+`"`)
		if got != want {
			t.Fatalf("candidate presence for %q = %v, want %v: %q", query, got, want, body)
		}
	}
	assertCandidate("Softmax", true)
	assertCandidate(target.ID[len(target.ID)-6:], true)

	linkBody := postJSON(t, baseURL+"/api/doc/"+docID+"/related", map[string]string{"target_id": target.ID})
	if !strings.Contains(string(linkBody), `"id":"`+target.ID+`"`) {
		t.Fatalf("unexpected link response: %q", linkBody)
	}

	listResp, err := http.Get(baseURL + "/api/doc/" + docID + "/related")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	if !strings.Contains(string(listBody), `"id":"`+target.ID+`"`) || !strings.Contains(string(listBody), `"direction":"out"`) {
		t.Fatalf("existing document missing from related list: %q", listBody)
	}

	// Already-related cards should not continue to appear in autocomplete.
	assertCandidate("Softmax", false)

	// The global document switcher uses the same picker but includes graph
	// neighbors; it only excludes the document currently being read.
	pickerCandidate := func(query, id string) bool {
		t.Helper()
		endpoint := baseURL + "/api/documents/candidates?purpose=open&focus=" + docID + "&q=" + query
		resp, getErr := http.Get(endpoint)
		if getErr != nil {
			t.Fatal(getErr)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("document picker failed: status=%d body=%q", resp.StatusCode, body)
		}
		return strings.Contains(string(body), `"id":"`+id+`"`)
	}
	if !pickerCandidate("Softmax", target.ID) {
		t.Fatal("global picker should include an already-related document")
	}
	if pickerCandidate("Flash", docID) {
		t.Fatal("global picker should exclude the current document")
	}
}

func TestServerRelatedCandidatesDefaultToRecentlyOpenedDocuments(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	createDocument := func(title string) string {
		t.Helper()
		body := postJSON(t, baseURL+"/api/save", map[string]string{
			"title": title,
			"body":  "# " + title + "\n",
		})
		var saved struct {
			ID string `json:"id"`
		}
		mustUnmarshal(t, body, &saved)
		return saved.ID
	}
	olderID := createDocument("Older Recent")
	newerID := createDocument("Newer Recent")
	unopenedID := createDocument("Not Opened Yet")
	for id, openedAt := range map[string]string{
		olderID: "2000-01-01T00:00:00.000Z",
		newerID: "2001-01-01T00:00:00.000Z",
	} {
		postJSON(t, baseURL+"/api/doc/"+id+"/annotations", map[string]any{
			"format":      "miru-annotations",
			"version":     2,
			"annotations": []any{},
			"progress":    map[string]any{"y": 0, "at": openedAt},
		})
	}

	listRecent := func() []struct {
		ID       string `json:"id"`
		OpenedAt string `json:"opened_at"`
	} {
		t.Helper()
		resp, err := http.Get(baseURL + "/api/doc/" + docID + "/related/candidates?limit=8")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("recent candidates failed: status=%d body=%q", resp.StatusCode, body)
		}
		var result struct {
			Candidates []struct {
				ID       string `json:"id"`
				OpenedAt string `json:"opened_at"`
			} `json:"candidates"`
		}
		mustUnmarshal(t, body, &result)
		return result.Candidates
	}

	recent := listRecent()
	if len(recent) != 2 || recent[0].ID != newerID || recent[1].ID != olderID {
		t.Fatalf("recent candidates are not newest first: %+v", recent)
	}
	if recent[0].OpenedAt == "" || recent[1].OpenedAt == "" {
		t.Fatalf("recent candidates do not include opened_at: %+v", recent)
	}

	// Loading a document counts as opening it even if the reader never scrolls.
	openedResp, err := http.Get(baseURL + "/api/doc/" + unopenedID)
	if err != nil {
		t.Fatal(err)
	}
	openedResp.Body.Close()
	recent = listRecent()
	if len(recent) != 3 || recent[0].ID != unopenedID {
		t.Fatalf("newly opened document is not first: %+v", recent)
	}
}

func TestServerRefusesDeletionForStaleRevision(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	sidecar := `{"format":"miru-annotations","version":2,"replaceAnnotations":true,"annotations":[
		{"start":0,"end":4,"exact":"body","highlight":true,"note":"first note"}
	]}`
	postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", json.RawMessage(sidecar))
	files, err := filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one note file, got %v (err=%v)", files, err)
	}

	// A tab that never saw this note (revision 0) tries to replace the whole
	// set with nothing — the deletion must be refused.
	stale := `{"format":"miru-annotations","version":2,"replaceAnnotations":true,"revision":0,"annotations":[]}`
	staleResult := postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", json.RawMessage(stale))
	if !strings.Contains(string(staleResult), `"replacement_applied":false`) {
		t.Fatalf("stale replacement conflict was not reported: %q", staleResult)
	}
	files, err = filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("stale client wiped notes: %v (err=%v)", files, err)
	}

	// With the current revision the same request is authorized.
	getResp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&view); err != nil {
		getResp.Body.Close()
		t.Fatal(err)
	}
	getResp.Body.Close()
	if view.Revision <= 0 {
		t.Fatalf("missing revision in GET: %+v", view)
	}
	fresh := fmt.Sprintf(`{"format":"miru-annotations","version":2,"replaceAnnotations":true,"revision":%d,"annotations":[]}`, view.Revision)
	freshResult := postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", json.RawMessage(fresh))
	if !strings.Contains(string(freshResult), `"replacement_applied":true`) {
		t.Fatalf("authorized replacement was not reported: %q", freshResult)
	}
	files, err = filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 0 {
		t.Fatalf("authorized replace left files: %v (err=%v)", files, err)
	}
}
