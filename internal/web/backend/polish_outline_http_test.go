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

func fakeOutlineOK(edits []polishOutlineEdit) polishOutlineFunc {
	return func(_ context.Context, _ string, _ string, prompt string) (string, string, error) {
		if !strings.Contains(prompt, "标题大纲") {
			return "", "", fmt.Errorf("prompt missing outline: %q", prompt)
		}
		var parts []string
		for _, e := range edits {
			parts = append(parts, fmt.Sprintf(`{"level":%d,"text":%q}`, e.Level, e.Text))
		}
		return "deepseek/deepseek-v4-flash", "根据语义分析……\n[" + strings.Join(parts, ",") + "]\n以上供参考", nil
	}
}

func TestPolishOutlineReturnsCorrectedEdits(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	restore := runPolishOutline
	runPolishOutline = fakeOutlineOK([]polishOutlineEdit{
		{Level: 1, Text: "Doc"},
		{Level: 2, Text: "Turso AgentFS：这个其实非常重要"}, // manual "1." stripped
		{Level: 2, Text: "B"},
		{Level: 3, Text: "B.1"},
	})
	defer func() { runPolishOutline = restore }()

	// The Miru header guards the endpoint against cross-origin pages.
	blocked := httptest.NewRecorder()
	server.handlePolishOutline(blocked, httptest.NewRequest(http.MethodPost, "/api/polish-outline", nil))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("without Miru header status=%d", blocked.Code)
	}

	body := `{"headings":[` +
		`{"level":1,"text":"Doc"},` +
		`{"level":2,"text":"1. Turso AgentFS：这个其实非常重要","sample":"GitHub：tursodatabase/agentfs"},` +
		`{"level":2,"text":"B"},` +
		`{"level":4,"text":"B.1"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/polish-outline", strings.NewReader(body))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handlePolishOutline(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var result struct {
		Edits []polishOutlineEdit `json:"edits"`
		Label string              `json:"label"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(result.Edits) != 4 || result.Edits[1].Text != "Turso AgentFS：这个其实非常重要" || result.Edits[3].Level != 3 {
		t.Fatalf("unexpected edits: %+v", result.Edits)
	}
	if result.Label == "" {
		t.Fatal("missing model label")
	}
}

// The section sample must reach the model — it is how garbled headings get
// a semantic rewrite.
func TestPolishOutlinePromptCarriesSamples(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	var capturedPrompt string
	restore := runPolishOutline
	runPolishOutline = func(_ context.Context, _ string, _ string, prompt string) (string, string, error) {
		capturedPrompt = prompt
		return "fake", `[{"level":2,"text":"Factory Vfs"}]`, nil
	}
	defer func() { runPolishOutline = restore }()

	body := `{"headings":[{"level":2,"text":"oss-filesystem-exploration","sample":"GitHub：Factory-AI/vfs 它是 Turso AgentFS 的 hard fork"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/polish-outline", strings.NewReader(body))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handlePolishOutline(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(capturedPrompt, "Factory-AI/vfs") || !strings.Contains(capturedPrompt, "采样") {
		t.Fatalf("sample missing from prompt: %q", capturedPrompt)
	}
}

func TestPolishOutlineRejectsBadInput(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	for _, body := range []string{
		`{}`,                                                            // no headings
		`{"headings":[{"level":0,"text":"x"}]}`,                         // level 0 without candidate flag
		`{"headings":[{"level":1,"text":"x","candidate":true}]}`,         // candidate must be level 0
		`{"headings":[{"level":7,"text":"x"}]}`,                         // level too high
		`{"headings":[{"level":2,"text":"` + strings.Repeat("x", 400) + `"}]}`,   // text too long
		`{"headings":[{"level":2,"text":"x","sample":"` + strings.Repeat("s", 300) + `"}]}`, // sample too long
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/polish-outline", strings.NewReader(body))
		request.Header.Set(miruClientHeader, "1")
		response := httptest.NewRecorder()
		server.handlePolishOutline(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status=%d, want 400", body, response.Code)
		}
	}
}

// A malformed or miscounted model answer must surface as 502, not be applied.
func TestPolishOutlineRejectsUnusableModelOutput(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	restore := runPolishOutline
	defer func() { runPolishOutline = restore }()

	for name, out := range map[string]string{
		"no array":    "我觉得应该是 2 级",
		"wrong count": `[{"level":2,"text":"A"}]`,
		"bad level":   `[{"level":2,"text":"A"},{"level":9,"text":"B"},{"level":2,"text":"C"}]`,
		"empty text":  `[{"level":2,"text":"A"},{"level":2,"text":" "},{"level":2,"text":"C"}]`,
	} {
		runPolishOutline = func(_ context.Context, _ string, _ string, _ string) (string, string, error) {
			return "fake", out, nil
		}
		request := httptest.NewRequest(http.MethodPost, "/api/polish-outline",
			strings.NewReader(`{"headings":[{"level":1,"text":"A"},{"level":2,"text":"B"},{"level":3,"text":"C"}]}`))
		request.Header.Set(miruClientHeader, "1")
		response := httptest.NewRecorder()
		server.handlePolishOutline(response, request)
		if response.Code != http.StatusBadGateway {
			t.Fatalf("%s: status=%d body=%q, want 502", name, response.Code, response.Body.String())
		}
	}
}

func TestParseOutlineEditsToleratesProseAndFences(t *testing.T) {
	headings := []polishOutlineHeading{{Level: 1, Text: "A"}, {Level: 2, Text: "B"}}
	edits, err := parseOutlineEdits("好的：```json\n[{\"level\":1,\"text\":\"A\"},{\"level\":2,\"text\":\"B\"}]\n```", headings)
	if err != nil {
		t.Fatal(err)
	}
	if edits[0].Level != 1 || edits[1].Text != "B" {
		t.Fatalf("unexpected edits: %+v", edits)
	}
}

// Pseudo-heading candidates take level 0 (left as body, text forced back to
// the original) or 1..6 (promoted); level 0 on a real heading stays a 502.
func TestParseOutlineEditsCandidates(t *testing.T) {
	headings := []polishOutlineHeading{
		{Level: 1, Text: "Doc"},
		{Level: 0, Text: "SOMEWHAT HAPPY", Candidate: true},
		{Level: 0, Text: "just a short line", Candidate: true},
	}
	edits, err := parseOutlineEdits(
		`[{"level":1,"text":"Doc"},{"level":2,"text":"SOMEWHAT HAPPY"},{"level":0,"text":"whatever the model wrote"}]`,
		headings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if edits[1].Level != 2 || edits[1].Text != "SOMEWHAT HAPPY" {
		t.Fatalf("promotion lost: %+v", edits[1])
	}
	if edits[2].Level != 0 || edits[2].Text != "just a short line" {
		t.Fatalf("body candidate must keep original text: %+v", edits[2])
	}

	if _, err := parseOutlineEdits(
		`[{"level":0,"text":"Doc"},{"level":2,"text":"SOMEWHAT HAPPY"},{"level":0,"text":"x"}]`,
		headings,
	); err == nil {
		t.Fatal("level 0 on a real heading must be rejected")
	}
}

func TestBuildOutlinePromptRendersCandidatesAndDividers(t *testing.T) {
	headings := []polishOutlineHeading{
		{Level: 3, Text: "Double Survivorship Biases", Empty: true},
		{Level: 2, Text: "Part I", Empty: true},
		{Level: 0, Text: "SOMEWHAT HAPPY", Candidate: true, Sample: "Marc lives on Park Avenue"},
	}
	prompt := buildOutlinePrompt(headings, true)
	if !strings.Contains(prompt, "（本节无正文——分隔大节）") {
		t.Fatalf("divider marker missing: %q", prompt)
	}
	if !strings.Contains(prompt, "（本节无正文——编号容器）") {
		t.Fatalf("numbered container marker missing: %q", prompt)
	}
	if !strings.Contains(prompt, "候选标题（正文行）「SOMEWHAT HAPPY」") {
		t.Fatalf("candidate marker missing: %q", prompt)
	}
	if !strings.Contains(prompt, "level\": 0") {
		t.Fatalf("output contract must mention level 0: %q", prompt)
	}
}

func TestLooksNumberedContainer(t *testing.T) {
	numbered := []string{"Part I", "Part 3", "Chapter 12", "Appendix B", "第3章", "第十二回", "Volume II"}
	plain := []string{"Double Survivorship Biases", "SOMEWHAT HAPPY", "章节小结", "转折"}
	for _, text := range numbered {
		if !looksNumberedContainer(text) {
			t.Errorf("%q should be a numbered container", text)
		}
	}
	for _, text := range plain {
		if looksNumberedContainer(text) {
			t.Errorf("%q must not be a numbered container", text)
		}
	}
}

func TestLooksGarbledHeading(t *testing.T) {
	garbled := []string{"oss-filesystem-exploration", "readme.md", "yt-ADzLv1nRtR8-transcript", "a_b"}
	clean := []string{"我会把版图扩成这样", "layerfs vs Vfs", "restic / Borg", "AgentFS", "3. Cloudflare Agents Workspace：另外一种答案"}
	for _, text := range garbled {
		if !looksGarbledHeading(text) {
			t.Errorf("%q should be detected as garbled", text)
		}
	}
	for _, text := range clean {
		if looksGarbledHeading(text) {
			t.Errorf("%q must not be flagged garbled", text)
		}
	}
}

// Over-budget outlines degrade gracefully: samples are dropped before the
// request reaches the model; only truly huge outlines get a 400.
func TestPolishOutlineDropsSamplesOverBudget(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetHome(t.TempDir())

	var capturedPrompt string
	restore := runPolishOutline
	runPolishOutline = func(_ context.Context, _ string, _ string, prompt string) (string, string, error) {
		capturedPrompt = prompt
		n := 0
		outline := prompt
		if idx := strings.LastIndex(prompt, "标题大纲："); idx >= 0 {
			outline = prompt[idx:]
		}
		for _, line := range strings.Split(outline, "\n") {
			if strings.Contains(line, ". H2 ") {
				n++
			}
		}
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, fmt.Sprintf(`{"level":2,"text":"t%d"}`, i))
		}
		return "fake", "[" + strings.Join(parts, ",") + "]", nil
	}
	defer func() { runPolishOutline = restore }()

	// 120 headings x max-length text+sample: over the sample budget, under
	// the headings-only cap.
	var sb strings.Builder
	sb.WriteString(`{"headings":[`)
	for i := 0; i < 120; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"level":2,"text":"t%d%s","sample":"%s"}`,
			i, strings.Repeat("x", 250), strings.Repeat("s", polishOutlineMaxSampleLen))
	}
	sb.WriteString(`]}`)

	request := httptest.NewRequest(http.MethodPost, "/api/polish-outline", strings.NewReader(sb.String()))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handlePolishOutline(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	outline := capturedPrompt
	if idx := strings.LastIndex(capturedPrompt, "标题大纲："); idx >= 0 {
		outline = capturedPrompt[idx:]
	}
	if strings.Contains(outline, "采样：") {
		t.Fatal("over-budget outline must drop samples before calling the model")
	}
	for i := 0; i < 120; i++ {
		if !strings.Contains(capturedPrompt, fmt.Sprintf("t%d%s", i, strings.Repeat("x", 50))) {
			t.Fatalf("heading %d missing from prompt", i)
		}
	}
}

func TestBuildOutlinePromptOmitsSamplesWhenDisabled(t *testing.T) {
	headings := []polishOutlineHeading{{Level: 1, Text: "A", Sample: "xyz"}}
	if got := buildOutlinePrompt(headings, false); strings.Contains(got, "xyz") {
		t.Fatalf("samples must be omitted: %q", got)
	}
}
