package backend

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"membox/internal/application"
)

// Full-document summarize jobs are serialized: the local mmd model handles
// one completion at a time, so parallel runs would just queue inside mmd.
var docSummarizeMu sync.Mutex

// handleDocumentSummarize map-reduces the whole document through the local
// model into a ≤1000-rune Markdown note linked to the source. Progress is
// streamed as NDJSON because long documents take minutes.
func (s *Server) handleDocumentSummarize(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if s.summarizer == nil {
		http.Error(writer, "local model (mmd) is not configured", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Force bool `json:"force"`
	}
	if request.Body != nil {
		// Empty body is fine; force defaults to false.
		_ = json.NewDecoder(request.Body).Decode(&payload)
	}
	if !docSummarizeMu.TryLock() {
		http.Error(writer, "another document summary is running", http.StatusConflict)
		return
	}
	defer docSummarizeMu.Unlock()

	writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	started := false
	emit := func(event map[string]any) {
		started = true
		if err := json.NewEncoder(writer).Encode(event); err != nil {
			return
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	result, err := s.service.SummarizeDocument(
		request.Context(),
		selector,
		payload.Force,
		s.summarizer.Complete,
		func(p application.SummarizeProgress) {
			emit(map[string]any{
				"type":  "progress",
				"stage": p.Stage,
				"index": p.Index,
				"total": p.Total,
				"depth": p.Depth,
			})
		},
	)
	if err != nil {
		if !started {
			docSummarizeMu.Unlock()
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		emit(map[string]any{"type": "error", "text": err.Error()})
		return
	}
	emit(map[string]any{
		"type":     "done",
		"id":       result.NoteID,
		"path":     result.Path,
		"title":    result.Title,
		"segments": result.Segments,
		"chars":    result.Chars,
		"existing": result.Existing,
	})
}
