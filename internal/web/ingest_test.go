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

func TestServerIngestPageOverwriteKeepsUUID(t *testing.T) {
	baseURL, _, _ := startServer(t)
	source := "https://example.com/idempotent"
	first := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "First", "body": "# First\n\nv1\n", "source_url": source, "clip_mode": "page",
	})
	var created struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	mustUnmarshal(t, first, &created)
	if created.ID == "" || !created.Created {
		t.Fatalf("first ingest: %+v", created)
	}

	// Second save without overwrite must conflict.
	data, _ := json.Marshal(map[string]any{
		"title": "Second", "body": "# Second\n\nv2\n", "source_url": source, "clip_mode": "page",
	})
	resp, err := http.Post(baseURL+"/api/ingest", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", resp.StatusCode, body)
	}
	var conflict struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Existing struct {
			ID string `json:"id"`
		} `json:"existing"`
	}
	mustUnmarshal(t, body, &conflict)
	if conflict.Error.Code != "clip_exists" || conflict.Existing.ID != created.ID {
		t.Fatalf("conflict payload: %s", body)
	}

	// Overwrite keeps UUID and replaces content.
	updated := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Second", "body": "# Second\n\nv2-final\n", "source_url": source, "clip_mode": "page",
		"overwrite": true,
	})
	var result struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
		Updated bool   `json:"updated"`
	}
	mustUnmarshal(t, updated, &result)
	if result.ID != created.ID || result.Created || !result.Updated {
		t.Fatalf("overwrite result: %+v want id=%s updated", result, created.ID)
	}
	docResp, err := http.Get(baseURL + "/api/doc/" + result.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer docResp.Body.Close()
	docBody, _ := io.ReadAll(docResp.Body)
	if !strings.Contains(string(docBody), "v2-final") {
		t.Fatalf("body not overwritten: %s", docBody)
	}
}

func TestServerIngestPageIdempotentAcrossOriginTrailingSlash(t *testing.T) {
	// Browsers/tab URLs disagree on https://host vs https://host/. Both must
	// hit the same page clip instead of creating title-2.md style duplicates.
	baseURL, _, _ := startServer(t)
	first := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Origin", "body": "# Origin\n\nv1\n", "source_url": "https://pi-from-scratch.vercel.app", "clip_mode": "page",
	})
	var created struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	mustUnmarshal(t, first, &created)
	if created.ID == "" || !created.Created {
		t.Fatalf("first ingest: %+v", created)
	}
	data, _ := json.Marshal(map[string]any{
		"title": "Origin again", "body": "# Origin\n\nv2\n", "source_url": "https://pi-from-scratch.vercel.app/", "clip_mode": "page",
	})
	resp, err := http.Post(baseURL+"/api/ingest", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for trailing-slash variant, got %d %s", resp.StatusCode, body)
	}
	var conflict struct {
		Existing struct {
			ID string `json:"id"`
		} `json:"existing"`
	}
	mustUnmarshal(t, body, &conflict)
	if conflict.Existing.ID != created.ID {
		t.Fatalf("conflict id=%q want %q (%s)", conflict.Existing.ID, created.ID, body)
	}
}

func TestIngestAfterTrashingPreviousClipCreatesFreshDocument(t *testing.T) {
	// Regression: a trashed page clip kept its document_sources row, so a new
	// save of the same URL was blocked by clip_exists pointing into the trash.
	ctx := context.Background()
	home := t.TempDir()
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "seed.md"), []byte("# Seed\n"), 0o600); err != nil {
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
	baseURL, err := server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })

	source := "https://example.com/trashed-clip"
	first := postJSON(t, baseURL+"/api/ingest", map[string]any{
		"title": "Trashed Page", "body": "# Trashed Page\n\nv1\n",
		"source_url": source, "clip_mode": "page",
	})
	var created struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, first, &created)
	if created.ID == "" {
		t.Fatal("first ingest missing id")
	}

	// User deletes the clip (soft delete into .membox-trash).
	if _, _, err := service.TrashDocumentFile(ctx, created.ID); err != nil {
		t.Fatalf("trash failed: %v", err)
	}

	// Same URL again, without overwrite: must create a fresh document, not 409.
	data, _ := json.Marshal(map[string]any{
		"title": "Trashed Page", "body": "# Trashed Page\n\nv2\n",
		"source_url": source, "clip_mode": "page",
	})
	resp, err := http.Post(baseURL+"/api/ingest", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-ingest after trash = %d %s, want 200 fresh create", resp.StatusCode, body)
	}
	var fresh struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
		Path    string `json:"path"`
	}
	mustUnmarshal(t, body, &fresh)
	if !fresh.Created || fresh.ID == "" || fresh.ID == created.ID {
		t.Fatalf("expected a new document, got %+v", fresh)
	}
	if strings.Contains(fresh.Path, ".membox-trash") {
		t.Fatalf("fresh clip landed in trash: %s", fresh.Path)
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
