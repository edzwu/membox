package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"membox/internal/translation"
)

type fakeTranslationStream struct{}

func (fakeTranslationStream) Stream(_ context.Context, request translation.Request, emit translation.EmitFunc) error {
	for _, event := range []translation.Event{
		{Type: "start", ID: request.ID, Provider: "ollama", Model: "qwen3:14b"},
		{Type: "delta", ID: request.ID, Text: "流式"},
		{Type: "delta", ID: request.ID, Text: "译文"},
		{Type: "done", ID: request.ID},
	} {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func TestTranslationHTTPStreamsNDJSONAndRequiresMiruHeader(t *testing.T) {
	server := NewServer(nil, nil, nil)
	server.SetTranslationStreamer(fakeTranslationStream{})
	payload, _ := json.Marshal(translation.Request{ID: "p-1", Text: "Streaming source paragraph."})

	blocked := httptest.NewRecorder()
	server.handleTranslationStream(blocked, httptest.NewRequest(http.MethodPost, "/api/translation/stream", strings.NewReader(string(payload))))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("without Miru header status=%d", blocked.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/translation/stream", strings.NewReader(string(payload)))
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleTranslationStream(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" {
		t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	body := response.Body.String()
	for _, marker := range []string{`"type":"start"`, `"text":"流式"`, `"text":"译文"`, `"type":"done"`} {
		if !strings.Contains(body, marker) {
			t.Fatalf("stream missing %s: %s", marker, body)
		}
	}
}
