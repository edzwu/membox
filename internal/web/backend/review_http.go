package backend

import (
	"encoding/json"

	"io"
	"net/http"
	"strconv"
	"strings"

	"membox/internal/application"
)

// handleReviewQueue returns the mixed review feed (new + due + aging cards),
// paged by limit/offset for infinite scroll.
func (s *Server) handleReviewQueue(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
	queue, err := s.service.ListReviewQueue(request.Context(), limit, offset)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(queue)
}

// handleReviewRate applies one spaced-review grade to a card.
func (s *Server) handleReviewRate(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "reading rate request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		NoteID string `json:"note_id"`
		Grade  string `json:"grade"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(writer, "invalid rate payload", http.StatusBadRequest)
		return
	}
	noteID := strings.TrimSpace(payload.NoteID)
	grade := application.ReviewGrade(strings.TrimSpace(payload.Grade))
	if noteID == "" {
		http.Error(writer, "note_id is required", http.StatusBadRequest)
		return
	}
	switch grade {
	case application.GradeAgain, application.GradeHard, application.GradeGood:
	default:
		http.Error(writer, "grade must be again, hard, or good", http.StatusBadRequest)
		return
	}
	schedule, err := s.service.RateReviewCard(request.Context(), noteID, grade)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusConflict)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(schedule)
}

// handleReviewReply appends a dated reply block to a note card.
func (s *Server) handleReviewReply(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 2<<20))
	if err != nil {
		http.Error(writer, "reading reply request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		NoteID string `json:"note_id"`
		Reply  string `json:"reply"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(writer, "invalid reply payload", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.NoteID) == "" {
		http.Error(writer, "note_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Reply) == "" {
		http.Error(writer, "reply is empty", http.StatusBadRequest)
		return
	}
	view, err := s.service.ReplyToReviewCard(request.Context(), payload.NoteID, payload.Reply)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(view)
}
