package daemon

import (
	"encoding/json"
	"net/http"
	"strings"

	"membox/internal/translation"
)

func (d *Daemon) handleTranslationStream(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, translation.MaxSegmentBytes+4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input translation.Request
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, "invalid translation request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if d.translator == nil {
		http.Error(writer, "translation service unavailable", http.StatusServiceUnavailable)
		return
	}
	// One local 14B generation at a time. A second Miru tab gets a fast,
	// explicit 429 instead of silently queuing behind a whole-document run.
	select {
	case d.translationSlot <- struct{}{}:
		defer func() { <-d.translationSlot }()
	default:
		http.Error(writer, "another translation is in progress", http.StatusTooManyRequests)
		return
	}

	writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	streamStarted := false
	emit := func(event translation.Event) error {
		streamStarted = true
		if err := json.NewEncoder(writer).Encode(event); err != nil {
			return err
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	if err := d.translator.Stream(request.Context(), input, emit); err != nil {
		if !streamStarted {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		_ = emit(translation.Event{Type: "error", ID: input.ID, Text: err.Error()})
	}
}

const maxCompletionPromptBytes = 128 << 10

// handleLLMComplete is a one-shot, non-streaming local-LLM prompt. It shares
// the single-flight slot with translation so only one qwen3:14b generation
// ever runs against the local GPU.
func (d *Daemon) handleLLMComplete(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxCompletionPromptBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Prompt) == "" {
		http.Error(writer, "invalid completion request", http.StatusBadRequest)
		return
	}
	if d.completer == nil {
		http.Error(writer, "completion service unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case d.translationSlot <- struct{}{}:
		defer func() { <-d.translationSlot }()
	default:
		http.Error(writer, "another local model request is in progress", http.StatusTooManyRequests)
		return
	}
	text, err := d.completer.Complete(request.Context(), input.Prompt)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]string{"text": text})
}
