package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"membox/internal/remotetopics"
)

// ---- DTOs -------------------------------------------------------------------

type topicItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

func toTopicItem(t remotetopics.Topic) topicItem {
	return topicItem{ID: t.ID, Title: t.Title, Description: t.Description,
		UpdatedAt: t.UpdatedAt.Format(time.RFC3339)}
}

type issueItem struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id,omitempty"`
	Title      string  `json:"title"`
	Body       string  `json:"body,omitempty"`
	Status     string  `json:"status"`
	ResolvedAt *string `json:"resolved_at,omitempty"`
	UpdatedAt  string  `json:"updated_at"`
}

func toIssueItem(i remotetopics.Issue) issueItem {
	item := issueItem{
		ID: i.ID, DocumentID: i.DocumentID, Title: i.Title, Body: i.Body,
		Status: i.Status, UpdatedAt: i.UpdatedAt.Format(time.RFC3339),
	}
	if i.ResolvedAt != nil {
		at := i.ResolvedAt.Format(time.RFC3339)
		item.ResolvedAt = &at
	}
	return item
}

func (s *Server) writeTopicError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remotetopics.ErrNotFound), errors.Is(err, remotetopics.ErrForeignEntity):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, remotetopics.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, remotetopics.ErrTitleRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// ---- topics -----------------------------------------------------------------

type topicBody struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Server) handleCreateTopic(w http.ResponseWriter, r *http.Request) {
	var body topicBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	topic, err := s.topics.CreateTopic(r.Context(), s.userFrom(r).ID, body.Title, body.Description)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTopicItem(topic))
}

func (s *Server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	topics, err := s.topics.ListTopics(r.Context(), s.userFrom(r).ID)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	items := make([]topicItem, 0, len(topics))
	for _, t := range topics {
		items = append(items, toTopicItem(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": items})
}

func (s *Server) handleUpdateTopic(w http.ResponseWriter, r *http.Request) {
	var body topicBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	topic, err := s.topics.UpdateTopic(r.Context(), s.userFrom(r).ID, r.PathValue("id"), body.Title, body.Description)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTopicItem(topic))
}

func (s *Server) handleDeleteTopic(w http.ResponseWriter, r *http.Request) {
	if err := s.topics.DeleteTopic(r.Context(), s.userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTopicMembers(w http.ResponseWriter, r *http.Request) {
	ids, err := s.topics.ListMembers(r.Context(), s.userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document_ids": ids})
}

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	err := s.topics.AddMember(r.Context(), s.userFrom(r).ID, r.PathValue("id"), r.PathValue("docID"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	err := s.topics.RemoveMember(r.Context(), s.userFrom(r).ID, r.PathValue("id"), r.PathValue("docID"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- issues -----------------------------------------------------------------

type issueBody struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	DocumentID string `json:"document_id,omitempty"`
	TopicID    string `json:"topic_id,omitempty"` // create-time convenience link
	Status     string `json:"status,omitempty"`   // update only
}

func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	var body issueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	issue, err := s.topics.CreateIssue(r.Context(), s.userFrom(r).ID,
		body.Title, body.Body, body.DocumentID, body.TopicID)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toIssueItem(issue))
}

func (s *Server) handleListIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := s.topics.ListIssues(r.Context(), s.userFrom(r).ID, r.URL.Query().Get("status"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	items := make([]issueItem, 0, len(issues))
	for _, i := range issues {
		items = append(items, toIssueItem(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": items})
}

func (s *Server) handleUpdateIssue(w http.ResponseWriter, r *http.Request) {
	var body issueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	issue, err := s.topics.UpdateIssue(r.Context(), s.userFrom(r).ID, r.PathValue("id"),
		body.Title, body.Body, body.Status)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toIssueItem(issue))
}

func (s *Server) handleDeleteIssue(w http.ResponseWriter, r *http.Request) {
	if err := s.topics.DeleteIssue(r.Context(), s.userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListTopicIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := s.topics.ListTopicIssues(r.Context(), s.userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	items := make([]issueItem, 0, len(issues))
	for _, i := range issues {
		items = append(items, toIssueItem(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": items})
}

func (s *Server) handleLinkIssue(w http.ResponseWriter, r *http.Request) {
	position, _ := strconv.Atoi(r.URL.Query().Get("position"))
	err := s.topics.LinkIssue(r.Context(), s.userFrom(r).ID, r.PathValue("id"), r.PathValue("issueID"), position)
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnlinkIssue(w http.ResponseWriter, r *http.Request) {
	err := s.topics.UnlinkIssue(r.Context(), s.userFrom(r).ID, r.PathValue("id"), r.PathValue("issueID"))
	if err != nil {
		s.writeTopicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// guard mirrors routes.go's docs guard: 503 until EnableTopics runs.
func (s *Server) topicsGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.topics == nil {
			writeError(w, http.StatusServiceUnavailable, "topics not configured")
			return
		}
		h(w, r)
	}
}
