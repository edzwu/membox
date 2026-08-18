package backend

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"membox/internal/application"
	"membox/internal/application/port"
)

type questionIngestRequest struct {
	Lines            []string `json:"lines"`
	SourceDocumentID string   `json:"source_document_id"`
	SourceFile       string   `json:"source_file"`
	SourceCommit     string   `json:"source_commit"`
}

// /api/questions is the question read model (open first). Optional filters:
// status, source_file, limit.
func (s *Server) handleQuestions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			http.Error(writer, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	sourceFile := strings.TrimSpace(request.URL.Query().Get("source_file"))

	var (
		questions []port.Question
		err       error
	)
	if sourceFile != "" {
		questions, err = s.service.ListQuestionsBySource(request.Context(), sourceFile, limit)
	} else {
		questions, err = s.service.ListQuestions(request.Context(), status, limit)
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if questions == nil {
		questions = []port.Question{}
	}
	writeQuestionJSON(writer, map[string]any{"questions": questions, "count": len(questions)})
}

// /api/questions/ingest extracts open questions from source lines (or an
// indexed source_file) and upserts them by canonical_body. Mirrors
// /api/resources/ingest: clients never write SQLite directly.
func (s *Server) handleQuestionIngest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	var payload questionIngestRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	var result application.QuestionIngestResult
	var err error
	if payload.Lines == nil {
		result, err = s.service.IngestQuestionDocument(
			request.Context(), payload.SourceDocumentID, payload.SourceFile, payload.SourceCommit,
		)
	} else {
		result, err = s.service.IngestQuestionLines(request.Context(), application.QuestionIngestOptions{
			Lines: payload.Lines, SourceDocumentID: payload.SourceDocumentID,
			SourceFile: payload.SourceFile, SourceCommit: payload.SourceCommit,
		})
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	all := append(append([]port.Question{}, result.Inserted...), result.Existing...)
	writeQuestionJSON(writer, map[string]any{
		"ok": true, "found": result.Found, "inserted": len(result.Inserted),
		"existing": len(result.Existing), "questions": all,
	})
}

func writeQuestionJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(payload)
}
