package backend

import (
	"encoding/json"
	"net/http"

	"membox/internal/translation"
)

func (s *Server) handleTranslationStream(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A custom header makes a cross-site form POST unable to spend local model
	// resources. Miru is same-origin and always supplies it.
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	if s.translator == nil {
		http.Error(writer, "translation unavailable: mmd is not configured", http.StatusServiceUnavailable)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, translation.MaxSegmentBytes+4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input translation.Request
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, "invalid translation request: "+err.Error(), http.StatusBadRequest)
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
	if err := s.translator.Stream(request.Context(), input, emit); err != nil {
		if !streamStarted {
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_ = emit(translation.Event{Type: "error", ID: input.ID, Text: err.Error()})
	}
}
