package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"membox/internal/application"
	"membox/internal/bootstrap"
)

func TestHandleVideoSummaryReusesExistingWithoutMMD(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	notes := t.TempDir()
	service, err := bootstrap.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.AddPath(ctx, notes); err != nil {
		t.Fatal(err)
	}

	body := "---\nkind: youtube-lecture-summary\nmanaged_by: echo-bp\ncourse_code: \"cs336\"\ncourse_id: \"PL\"\ncourse_title: \"CS336\"\nlecture_no: 2\nlecture_no_source: \"title\"\nplaylist_index: 2\nvideo_id: \"abcVIDEO12\"\ntitle: \"Tokenizer\"\nsource_url: \"https://www.youtube.com/watch?v=abcVIDEO12\"\n---\n\n# Tokenizer\n\nSummary body.\n"
	if _, err := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{
		Filename: "cs336-lec2.md", Body: body,
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServer(service, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}, fstest.MapFS{})
	server.SetHome(home)

	req := httptest.NewRequest(http.MethodPost, "/api/video/summary", strings.NewReader(
		`{"url":"https://youtu.be/abcVIDEO12?list=PLxx"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleVideoSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result videoSummaryHTTPResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.VideoID != "abcVIDEO12" || result.ID == "" || result.ViewURL == "" {
		t.Fatalf("result = %+v", result)
	}
	if result.Created || result.Filename != "cs336-lec2.md" {
		t.Fatalf("expected existing filename/id reuse: %+v", result)
	}
	if !strings.Contains(result.ViewURL, result.ID) {
		t.Fatalf("view_url should embed id: %+v", result)
	}
}

func TestHandleVideoSummaryLookupOnlyMisses(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	notes := t.TempDir()
	service, err := bootstrap.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.AddPath(ctx, notes); err != nil {
		t.Fatal(err)
	}
	server := NewServer(service, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}, fstest.MapFS{})
	server.SetHome(home)
	req := httptest.NewRequest(http.MethodPost, "/api/video/summary", strings.NewReader(
		`{"url":"https://www.youtube.com/watch?v=missingVid1","lookup_only":true}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleVideoSummary(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleVideoSummaryRejectsNonYouTube(t *testing.T) {
	server := NewServer(nil, fstest.MapFS{}, fstest.MapFS{})
	server.SetHome(t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/video/summary", strings.NewReader(
		`{"url":"https://example.com/watch?v=nope"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleVideoSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "not a YouTube") {
		t.Fatalf("body=%s", body)
	}
}

func TestHandleVideoSummaryRequiresHome(t *testing.T) {
	server := NewServer(nil, fstest.MapFS{}, fstest.MapFS{})
	req := httptest.NewRequest(http.MethodPost, "/api/video/summary", strings.NewReader(
		`{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleVideoSummary(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
