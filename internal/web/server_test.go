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
	records, err := service.ListDocuments(ctx, 10, false, "")
	if err != nil || len(records) != 1 {
		t.Fatalf("expected one indexed document, got %d (err=%v)", len(records), err)
	}
	physicalID := string(records[0].Document.ID)
	logical, logicalErr := service.LogicalID(ctx, physicalID)
	if logicalErr != nil || logical == "" {
		t.Fatalf("logical id for %s: %v (%q)", physicalID, logicalErr, logical)
	}
	docID = logical

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
	if !strings.Contains(string(indexBody), `src="/adapters/companion/integration.js"`) {
		t.Fatalf("index.html missing isolated membox adapter: %s", indexBody[:200])
	}
	if strings.Contains(string(indexBody), "brand-social") || !strings.Contains(string(indexBody), `class="topbar-center"`) {
		t.Fatal("host-neutral topbar should expose an empty center slot without personal links")
	}
	if !strings.Contains(string(indexBody), `id="reading-surface"`) || !strings.Contains(string(indexBody), `id="annotation-layer"`) {
		t.Fatal("reader should keep article content and annotation cards in separate sibling layers")
	}
	adapterResp, err := http.Get(baseURL + "/adapters/companion/integration.js")
	if err != nil {
		t.Fatal(err)
	}
	adapterBody, _ := io.ReadAll(adapterResp.Body)
	adapterResp.Body.Close()
	if adapterResp.StatusCode != http.StatusOK || !strings.Contains(string(adapterBody), "./reading-state.js") || !strings.Contains(string(adapterBody), "./pdf-import.js") || !strings.Contains(string(adapterBody), "./translation.js") || !strings.Contains(string(adapterBody), "./jp-study.js") {
		t.Fatalf("membox adapter composition root unavailable: status=%d", adapterResp.StatusCode)
	}
	// The adapter is split into cohesive feature modules; each must be served.
	featureModules := map[string][]string{
		"connection.js":  {"membox-connection"},
		"document.js":    {"membox-document-switcher", "Switch document · Ctrl+O", "membox-document-navigation"},
		"notes.js":       {"membox-browse-notes", "Notes in this document", "noteDocumentURL"},
		"note-images.js": {"registerAnnotImagePasteHandler", "uploadNoteImage", "/api/note-assets/"},
		"pdf-import.js":  {"Drop one PDF at a time", "importPDF(file)"},
		"translation.js": {"streamTranslation", "runSelectionRows", "registerAnnotAction"},
		"jp-study.js":    {"membox-jp-study-toggle", "mode: 'jp-study'", "日语精读"},
	}
	for module, markers := range featureModules {
		moduleResp, err := http.Get(baseURL + "/adapters/companion/" + module)
		if err != nil {
			t.Fatal(err)
		}
		moduleBody, _ := io.ReadAll(moduleResp.Body)
		moduleResp.Body.Close()
		if moduleResp.StatusCode != http.StatusOK {
			t.Fatalf("membox adapter module %s unavailable: status=%d", module, moduleResp.StatusCode)
		}
		for _, marker := range markers {
			if !strings.Contains(string(moduleBody), marker) {
				t.Fatalf("membox adapter module %s missing %q", module, marker)
			}
		}
	}

	// The PDF import route must exist, but rejects form-style cross-site POSTs
	// that cannot carry Miru's custom same-origin header.
	pdfImportResp, err := http.Post(baseURL+"/api/pdfs/import", "multipart/form-data", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	pdfImportResp.Body.Close()
	if pdfImportResp.StatusCode != http.StatusForbidden {
		t.Fatalf("PDF import route status=%d, want forbidden without Miru header", pdfImportResp.StatusCode)
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

func TestServerRenamesDocumentWithoutChangingIdentity(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	body := postJSON(t, baseURL+"/api/doc/"+docID+"/rename", map[string]string{
		"filename": "renamed-article.md",
		"title":    "Renamed Article",
	})
	var result struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Title    string `json:"title"`
	}
	mustUnmarshal(t, body, &result)
	if result.ID != docID || result.Filename != "renamed-article.md" || result.Title != "Renamed Article" {
		t.Fatalf("unexpected rename result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(notesDir, "flash.md")); !os.IsNotExist(err) {
		t.Fatalf("old filename still exists: %v", err)
	}
	renamedPath := filepath.Join(notesDir, "renamed-article.md")
	if _, err := os.Stat(renamedPath); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	saved, err := os.ReadFile(renamedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "# Renamed Article") {
		t.Fatalf("body H1 not updated after rename: %s", saved)
	}

	resp, err := http.Get(baseURL + "/api/doc/" + docID)
	if err != nil {
		t.Fatal(err)
	}
	docBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := resp.Header.Get("X-Membox-Filename"); got != "renamed-article.md" {
		t.Fatalf("renamed filename header = %q", got)
	}
	if titleHdr := resp.Header.Get("X-Membox-Title"); titleHdr != "" {
		decoded, decErr := url.PathUnescape(titleHdr)
		if decErr != nil {
			decoded = titleHdr
		}
		if decoded != "Renamed Article" {
			t.Fatalf("X-Membox-Title = %q", titleHdr)
		}
	}
	if !strings.Contains(string(docBody), "# Renamed Article") {
		t.Fatalf("reopened body missing new H1: %s", docBody)
	}
}

func TestServerSerializesAnnotationRestoreWithRename(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	for index := 0; index < 12; index++ {
		filename := fmt.Sprintf("concurrent-%02d.md", index)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			resp, err := http.Get(baseURL + "/api/doc/" + docID + "/annotations")
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
					body, _ := io.ReadAll(resp.Body)
					err = fmt.Errorf("annotation restore status=%d body=%q", resp.StatusCode, body)
				}
			}
			results <- err
		}()
		go func() {
			<-start
			payload, _ := json.Marshal(map[string]string{"filename": filename})
			resp, err := http.Post(baseURL+"/api/doc/"+docID+"/rename", "application/json", strings.NewReader(string(payload)))
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(resp.Body)
					err = fmt.Errorf("rename status=%d body=%q", resp.StatusCode, body)
				}
			}
			results <- err
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
	}

	resp, err := http.Get(baseURL + "/api/doc/" + docID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Membox-Filename"); got != "concurrent-11.md" {
		t.Fatalf("final filename = %q, want concurrent-11.md", got)
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
	records, err := service.ListDocuments(ctx, 10, false, "")
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

	records, err = service.ListDocuments(ctx, 10, false, "")
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

func TestSelectionNoteRelatedSourceCarriesReturnAnchor(t *testing.T) {
	baseURL, docID, _ := startServer(t)

	savedBody := postJSON(t, baseURL+"/api/doc/"+docID+"/annotations", map[string]any{
		"format":  "miru-annotations",
		"version": 2,
		"annotations": []map[string]any{{
			"start": 18, "exact": "body", "prefix": "Flash Attention\n", "suffix": "",
			"note": "A long explanation with its own quoted example.\n\n> quoted inside the note",
		}},
	})
	var saved struct {
		Annotations []struct {
			Ref string `json:"ref"`
		} `json:"annotations"`
	}
	mustUnmarshal(t, savedBody, &saved)
	if len(saved.Annotations) != 1 || saved.Annotations[0].Ref == "" {
		t.Fatalf("annotation note was not created: %q", savedBody)
	}
	noteID := saved.Annotations[0].Ref

	response, err := http.Get(baseURL + "/api/doc/" + noteID + "/related")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("selection-note related list failed: status=%d body=%q", response.StatusCode, body)
	}
	var related struct {
		Related []struct {
			ID            string `json:"id"`
			Title         string `json:"title"`
			AnnotationRef string `json:"annotation_ref"`
		} `json:"related"`
	}
	mustUnmarshal(t, body, &related)
	if len(related.Related) != 1 || related.Related[0].AnnotationRef != noteID {
		t.Fatalf("source return anchor missing from related list: %q", body)
	}
	// Source card is identified by logical id (may lengthen after the note is
	// created if tails collided); title is stable.
	if related.Related[0].Title != "Flash Attention" || related.Related[0].ID == "" || related.Related[0].ID == noteID {
		t.Fatalf("source return card wrong: %q", body)
	}
	docID = related.Related[0].ID

	openPickerIDs := func(query string) []string {
		t.Helper()
		endpoint := baseURL + "/api/documents/candidates?purpose=open&focus=" + url.QueryEscape(docID) + "&q=" + url.QueryEscape(query)
		response, getErr := http.Get(endpoint)
		if getErr != nil {
			t.Fatal(getErr)
		}
		defer response.Body.Close()
		var result struct {
			Candidates []struct {
				ID string `json:"id"`
			} `json:"candidates"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&result); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		ids := make([]string, 0, len(result.Candidates))
		for _, candidate := range result.Candidates {
			ids = append(ids, candidate.ID)
		}
		return ids
	}
	containsID := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	// Ctrl+O matches the visible logical id left-to-right (not physical UUID tails).
	if ids := openPickerIDs(noteID); !containsID(ids, noteID) {
		t.Fatalf("Ctrl+O logical id did not find selection note %s: %v", noteID, ids)
	}
	if len(noteID) > 2 {
		if ids := openPickerIDs(noteID[:len(noteID)-1]); !containsID(ids, noteID) {
			t.Fatalf("Ctrl+O logical prefix did not find selection note %s: %v", noteID, ids)
		}
	}
	if ids := openPickerIDs(docID); !containsID(ids, docID) {
		t.Fatalf("Ctrl+O logical id did not find its current document %s: %v", docID, ids)
	}
	if ids := openPickerIDs("body"); containsID(ids, noteID) {
		t.Fatalf("Ctrl+O exposed an internal selection note through non-UUID text: %v", ids)
	}
}

func TestServerRelatedCreatesLinkedPlainDocument(t *testing.T) {
	baseURL, docID, notesDir := startServer(t)

	// The modal title is authoritative even when pasted Markdown carries a
	// different H1. It generates the filename and must also be the title shown
	// when the new document is opened in Miru.
	payload := `{"title":"Related Idea","body":"# Pasted Heading\n\nSome related content."}`
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
		ID    string `json:"id"`
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("unexpected create response: %q", body)
	}
	if created.Title != "Related Idea" {
		t.Fatalf("created title = %q, want modal title", created.Title)
	}
	// A plain Markdown document, not a selection note.
	base := filepath.Base(created.Path)
	if strings.HasSuffix(base, "-note.md") {
		t.Fatalf("related document uses note naming: %s", base)
	}
	createdPath := filepath.Join(notesDir, base)
	if _, err := os.Stat(createdPath); err != nil {
		t.Fatalf("related file missing: %v", err)
	}
	stored, err := os.ReadFile(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(stored), "# Related Idea\n") || strings.Contains(string(stored), "# Pasted Heading") {
		t.Fatalf("created Markdown did not align its H1 with the modal title: %q", stored)
	}
	openResp, err := http.Get(baseURL + "/api/doc/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, openResp.Body)
	openResp.Body.Close()
	shownTitle, err := url.PathUnescape(openResp.Header.Get("X-Membox-Title"))
	if err != nil {
		t.Fatal(err)
	}
	if shownTitle != "Related Idea" {
		t.Fatalf("web title header = %q, want modal title", shownTitle)
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
	assertCandidate(target.ID, true)
	if len(target.ID) > 2 {
		assertCandidate(target.ID[:len(target.ID)-1], true)
	}

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
	if len(recent) != 3 || recent[0].ID != newerID || recent[1].ID != olderID || recent[2].ID != unopenedID {
		t.Fatalf("picker should keep recently opened first and fill from membox: %+v", recent)
	}
	if recent[0].OpenedAt == "" || recent[1].OpenedAt == "" || recent[2].OpenedAt != "" {
		t.Fatalf("picker did not distinguish opened history from modified fallback: %+v", recent)
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
