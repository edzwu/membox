package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

func TestMiruAnnotationsMaterializeAsMarkdownNoteDocuments(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)
	payload := map[string]any{
		"format": "miru-annotations", "version": 2,
		"replaceAnnotations": true,
		"annotations": []map[string]any{{
			"start": 16, "exact": "body", "prefix": "Flash Attention ", "suffix": "",
			"highlight": true, "underline": false, "strikethrough": false,
			"note": "Remember this passage.",
		}},
		"progress": map[string]any{"y": 12, "at": "2026-08-04T00:00:00Z"},
	}
	postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", payload)
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
	}
	if err := json.NewDecoder(resp2.Body).Decode(&edited); err != nil {
		resp2.Body.Close()
		t.Fatal(err)
	}
	resp2.Body.Close()
	if len(edited.Annotations) != 1 || edited.Annotations[0].Note != "Edited outside Miru." {
		t.Fatalf("external Markdown edit not reflected: %+v", edited)
	}

	postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", map[string]any{
		"format": "miru-annotations", "version": 2, "replaceAnnotations": true,
		"annotations": []any{}, "progress": map[string]any{"y": 13, "at": "2026-08-04T00:01:00Z"},
	})
	files, err = filepath.Glob(filepath.Join(notesDir, "*-note.md"))
	if err != nil || len(files) != 0 {
		t.Fatalf("deleted annotation note files=%v err=%v", files, err)
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
