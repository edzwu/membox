package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSuggestTitleReturnsCleanTitle(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	restore := runSuggestTitle
	runSuggestTitle = func(_ context.Context, _ string, _ string, prompt string) (string, string, error) {
		for _, want := range []string{"当前标题：oss-filesystem-exploration", "导语：", "标题大纲：", "H2 Factory Vfs"} {
			if !strings.Contains(prompt, want) {
				return "", "", fmt.Errorf("prompt missing %q: %q", want, prompt)
			}
		}
		// Prose + quotes + trailing punctuation must be cleaned off.
		return "deepseek/deepseek-v4-flash", "好的，标题是：《Agent 文件系统版图》。\n希望有帮助！", nil
	}
	defer func() { runSuggestTitle = restore }()

	// The Miru header guards the endpoint against cross-origin pages.
	blocked := httptest.NewRecorder()
	server.handleSuggestTitle(blocked, httptest.NewRequest(http.MethodPost, "/api/suggest-title", nil))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("without Miru header status=%d", blocked.Code)
	}

	body := `{"current":"oss-filesystem-exploration","lead":"我按你这套 CAS 的坐标系挖了一轮",` +
		`"headings":[{"level":1,"text":"我会把版图扩成这样"},{"level":2,"text":"Factory Vfs"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/suggest-title", strings.NewReader(body))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleSuggestTitle(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var result struct {
		Title string `json:"title"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	// First line only, book-title marks and trailing punctuation stripped.
	if result.Title != "Agent 文件系统版图" {
		t.Fatalf("unexpected title: %q", result.Title)
	}
	if result.Label == "" {
		t.Fatal("missing model label")
	}
}

func TestSuggestTitleRejectsBadInput(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	for _, body := range []string{
		`{"headings":[]}`, // outline is required
		`{"headings":[{"level":2,"text":"x"}],"lead":"` + strings.Repeat("l", 600) + `"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/suggest-title", strings.NewReader(body))
		request.Header.Set(miruClientHeader, "1")
		response := httptest.NewRecorder()
		server.handleSuggestTitle(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.60s: status=%d, want 400", body, response.Code)
		}
	}
}

func TestSuggestTitleRejectsUnusableModelOutput(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	restore := runSuggestTitle
	defer func() { runSuggestTitle = restore }()

	for name, out := range map[string]string{
		"empty":      "   ",
		"too long":   strings.Repeat("长", 100),
		"only marks": "《》。",
	} {
		runSuggestTitle = func(_ context.Context, _ string, _ string, _ string) (string, string, error) {
			return "fake", out, nil
		}
		request := httptest.NewRequest(http.MethodPost, "/api/suggest-title",
			strings.NewReader(`{"headings":[{"level":1,"text":"A"}]}`))
		request.Header.Set(miruClientHeader, "1")
		response := httptest.NewRecorder()
		server.handleSuggestTitle(response, request)
		if response.Code != http.StatusBadGateway {
			t.Fatalf("%s: status=%d body=%q, want 502", name, response.Code, response.Body.String())
		}
	}
}

func TestCleanSuggestedTitleUnwrapsJSONArray(t *testing.T) {
	title, err := cleanSuggestedTitle(`["agent 文件系统与 memory 开源版图"]`)
	if err != nil || title != "agent 文件系统与 memory 开源版图" {
		t.Fatalf("title=%q err=%v", title, err)
	}
}
