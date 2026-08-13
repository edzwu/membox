package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"membox/internal/bootstrap"
	"membox/internal/web"
)

func TestResourceAPIIngestAssessListAndScanCursor(t *testing.T) {
	baseURL, _, notesDir := startServer(t)
	ingested := postJSON(t, baseURL+"/api/resources/ingest", map[string]any{
		"lines": []string{
			"- [Core](https://example.com/core?utm_source=inbox)",
			"- duplicate https://example.com/core#section",
		},
		"source_file": "inbox.md", "source_commit": "deadbeef",
	})
	var ingest struct {
		Found     int `json:"found"`
		Inserted  int `json:"inserted"`
		Existing  int `json:"existing"`
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	mustUnmarshal(t, ingested, &ingest)
	if ingest.Found != 1 || ingest.Inserted != 1 || ingest.Existing != 0 || len(ingest.Resources) != 1 {
		t.Fatalf("ingest = %+v", ingest)
	}

	// Full scans send only an indexed source file; the extension never reads
	// the file or performs URL extraction itself.
	inboxPath := filepath.Join(notesDir, "flash.md")
	if err := os.WriteFile(inboxPath, []byte("# Inbox\n\nhttps://example.com/core#again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromDocument := postJSON(t, baseURL+"/api/resources/ingest", map[string]any{
		"source_file": inboxPath,
	})
	var documentIngest struct {
		Found    int `json:"found"`
		Inserted int `json:"inserted"`
		Existing int `json:"existing"`
	}
	mustUnmarshal(t, fromDocument, &documentIngest)
	if documentIngest.Found != 1 || documentIngest.Inserted != 0 || documentIngest.Existing != 1 {
		t.Fatalf("document ingest = %+v", documentIngest)
	}
	filtered, err := http.Get(baseURL + "/api/resources?limit=10&source_file=" + url.QueryEscape(inboxPath))
	if err != nil {
		t.Fatal(err)
	}
	var filteredList struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(filtered.Body).Decode(&filteredList); err != nil {
		filtered.Body.Close()
		t.Fatal(err)
	}
	filtered.Body.Close()
	if filteredList.Count != 1 {
		t.Fatalf("source occurrence was not retained: %+v", filteredList)
	}

	expected := 1
	assessed := postJSON(t, baseURL+"/api/resources/assess", map[string]any{
		"assessments": []map[string]any{{
			"id": ingest.Resources[0].ID, "priority": "H", "score": 0.91,
			"reason": "primary source for current research",
		}},
		"wave": 1, "source": "inbox.md", "expected_count": expected,
	})
	var assessment struct {
		Assessed int `json:"assessed"`
	}
	mustUnmarshal(t, assessed, &assessment)
	if assessment.Assessed != 1 {
		t.Fatalf("assessment = %+v", assessment)
	}

	resp, err := http.Get(baseURL + "/api/resources?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, body)
	}
	var listed struct {
		Count     int `json:"count"`
		Resources []struct {
			Priority string   `json:"priority"`
			Score    *float64 `json:"score"`
		} `json:"resources"`
	}
	mustUnmarshal(t, body, &listed)
	if listed.Count != 1 || listed.Resources[0].Priority != "H" || listed.Resources[0].Score == nil || *listed.Resources[0].Score != 0.91 {
		t.Fatalf("list = %+v", listed)
	}

	cursor, err := http.Get(baseURL + "/api/resources/scan?source=inbox.md")
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Body.Close()
	var state struct {
		Wave int `json:"wave"`
	}
	if err := json.NewDecoder(cursor.Body).Decode(&state); err != nil || state.Wave != 1 {
		t.Fatalf("scan state = %+v err=%v", state, err)
	}
}

func TestResourceAPIRequiresBridgeToken(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(notes, "seed.md"), []byte("# Seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := bootstrap.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.AddPath(ctx, notes); err != nil {
		t.Fatal(err)
	}
	server := web.NewServer(service)
	server.SetToken("resource-secret")
	baseURL, err := server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })
	resp, err := http.Post(baseURL+"/api/resources/ingest", "application/json", strings.NewReader(`{"lines":["https://example.com"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}
