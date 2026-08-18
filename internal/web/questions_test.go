package web_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"membox/internal/bootstrap"
	"membox/internal/web"
)

func TestQuestionAPIIngestAndList(t *testing.T) {
	baseURL, _, _ := startServer(t)
	ingested := postJSON(t, baseURL+"/api/questions/ingest", map[string]any{
		"lines": []string{
			"- 需要关注, pi 的 cache 到底是怎么做的?",
			"- Q: 为什么 FTS 默认 AND",
			"- opencli?",
			"* 需要关注, pi 的 cache 到底是怎么做的?",
		},
		"source_file": "inbox.md", "source_commit": "deadbeef",
	})
	var ingest struct {
		Found     int `json:"found"`
		Inserted  int `json:"inserted"`
		Existing  int `json:"existing"`
		Questions []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"questions"`
	}
	mustUnmarshal(t, ingested, &ingest)
	if ingest.Found != 2 || ingest.Inserted != 2 || ingest.Existing != 0 || len(ingest.Questions) != 2 {
		t.Fatalf("ingest = %+v", ingest)
	}

	again := postJSON(t, baseURL+"/api/questions/ingest", map[string]any{
		"lines": []string{
			"- 需要关注, pi 的 cache 到底是怎么做的?",
		},
		"source_file": "inbox.md", "source_commit": "cafebabe",
	})
	var second struct {
		Inserted int `json:"inserted"`
		Existing int `json:"existing"`
	}
	mustUnmarshal(t, again, &second)
	if second.Inserted != 0 || second.Existing != 1 {
		t.Fatalf("second ingest = %+v", second)
	}

	resp, err := http.Get(baseURL + "/api/questions?limit=10&source_file=inbox.md")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, listed)
	}
	var list struct {
		Count     int `json:"count"`
		Questions []struct {
			Body         string `json:"body"`
			SourceCommit string `json:"source_commit"`
		} `json:"questions"`
	}
	mustUnmarshal(t, listed, &list)
	if list.Count != 2 {
		t.Fatalf("list = %+v", list)
	}
	foundRefresh := false
	for _, q := range list.Questions {
		if strings.Contains(q.Body, "cache") && q.SourceCommit == "cafebabe" {
			foundRefresh = true
		}
	}
	if !foundRefresh {
		t.Fatalf("expected provenance refresh on list: %+v", list.Questions)
	}
}

func TestQuestionAPIRequiresBridgeToken(t *testing.T) {
	ctx := context.Background()
	home, notes := t.TempDir(), t.TempDir()
	service, err := bootstrap.Open(home + "/membox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.AddPath(ctx, notes); err != nil {
		t.Fatal(err)
	}
	server := web.NewServer(service)
	server.SetToken("question-secret")
	baseURL, err := server.Start(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })
	resp, err := http.Post(baseURL+"/api/questions/ingest", "application/json",
		strings.NewReader(`{"lines":["- why does this work?"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}
