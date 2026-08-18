package backend

import (
	"net/http"
	"strings"

	"membox/internal/agent"
)

func (s *Server) requireAgentWorker(writer http.ResponseWriter, request *http.Request) (*agent.ManagerHandle, bool) {
	// Internal endpoints never allow browser/extension origins.
	if origin := request.Header.Get("Origin"); origin != "" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return nil, false
	}
	if site := request.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-origin" || site == "same-site" {
		// Browsers set this; Pi workers do not.
		http.Error(writer, "forbidden", http.StatusForbidden)
		return nil, false
	}
	token := bearerToken(request)
	workerID := strings.TrimSpace(request.Header.Get("X-Membox-Worker-Id"))
	sessionID := strings.TrimSpace(request.Header.Get("X-Membox-Session-Id"))
	if token == "" || workerID == "" || sessionID == "" {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	mgr := s.agentManager()
	if mgr == nil || !mgr.ValidateWorker(workerID, sessionID, token) {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return &agent.ManagerHandle{Manager: mgr, SessionID: sessionID, WorkerID: workerID}, true
}

func (s *Server) handleAgentInternalContext(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	entry, found := handle.Manager.TakeRunContext(handle.SessionID)
	if !found {
		// No document context for this turn is valid.
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion})
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{
		"v":           agentAPIVersion,
		"document_id": entry.DocumentID,
		"title":       entry.Title,
		"status":      entry.Status,
	})
}

func (s *Server) handleAgentInternalSearch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	tools := handle.Manager.ToolsFor()
	if tools == nil {
		writeAgentError(writer, agent.NewInvalid("tools unavailable"))
		return
	}
	hits, err := tools.SearchDocuments(request.Context(), body.Query, body.Limit)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "hits": hits})
}

func (s *Server) handleAgentInternalGrep(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	tools := handle.Manager.ToolsFor()
	if tools == nil {
		writeAgentError(writer, agent.NewInvalid("tools unavailable"))
		return
	}
	hits, err := tools.GrepDocuments(request.Context(), body.Pattern, body.Limit)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "hits": hits})
}

func (s *Server) handleAgentInternalRead(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		ID     string `json:"id"`
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	if !looksLikeUUID(body.ID) {
		writeAgentError(writer, agent.NewInvalid("document id must be a UUID"))
		return
	}
	tools := handle.Manager.ToolsFor()
	chunk, err := tools.ReadDocument(request.Context(), body.ID, body.Cursor, body.Limit)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "document": chunk})
}

func (s *Server) handleAgentInternalGet(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	if !looksLikeUUID(body.ID) {
		writeAgentError(writer, agent.NewInvalid("document id must be a UUID"))
		return
	}
	view, err := handle.Manager.ToolsFor().GetDocument(request.Context(), body.ID)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	// Never return absolute path to the model as a writable path; keep relative-ish path only if needed.
	view.Path = ""
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "document": view})
}

func (s *Server) handleAgentInternalRelated(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	if !looksLikeUUID(body.ID) {
		writeAgentError(writer, agent.NewInvalid("document id must be a UUID"))
		return
	}
	view, err := handle.Manager.ToolsFor().ListRelated(request.Context(), body.ID)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "related": view})
}

func (s *Server) handleAgentInternalWrite(writer http.ResponseWriter, request *http.Request, kind string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	if !handle.Manager.WriteToolsEnabled() {
		writeAgentError(writer, agent.NewWriteDisabled())
		return
	}
	tools := handle.Manager.ToolsFor()
	if tools == nil {
		writeAgentError(writer, agent.NewInvalid("tools unavailable"))
		return
	}
	ctx := request.Context()
	switch kind {
	case "create_note":
		var body agent.CreateNoteCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if body.TargetID != "" && !looksLikeUUID(body.TargetID) {
			writeAgentError(writer, agent.NewInvalid("target_id must be a UUID"))
			return
		}
		result, err := tools.CreateNote(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	case "update":
		var body agent.UpdateDocumentCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if !looksLikeUUID(body.ID) {
			writeAgentError(writer, agent.NewInvalid("document id must be a UUID"))
			return
		}
		result, err := tools.UpdateDocument(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	case "rename":
		var body agent.RenameDocumentCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if !looksLikeUUID(body.ID) {
			writeAgentError(writer, agent.NewInvalid("document id must be a UUID"))
			return
		}
		result, err := tools.RenameDocument(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	case "link":
		var body agent.LinkDocumentsCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if !looksLikeUUID(body.FromID) || !looksLikeUUID(body.ToID) {
			writeAgentError(writer, agent.NewInvalid("from_id and to_id must be UUIDs"))
			return
		}
		result, err := tools.LinkDocuments(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	default:
		writeAgentError(writer, agent.NewInvalid("unknown write tool"))
	}
}

func looksLikeUUID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 8 {
		return false
	}
	for _, c := range id {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// handleAgentInternalListQuestions is a read-only question listing tool.
func (s *Server) handleAgentInternalListQuestions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := decodeJSONBody(request, &body); err != nil {
		writeAgentError(writer, agent.NewInvalid(err.Error()))
		return
	}
	tools := handle.Manager.ToolsFor()
	if tools == nil {
		writeAgentError(writer, agent.NewInvalid("tools unavailable"))
		return
	}
	questions, err := tools.ListQuestions(request.Context(), body.Status, body.Limit)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "questions": questions})
}

// handleAgentInternalQuestionWrite handles add/answer/delete question tools
// through the same approval-gated write path as the document write tools.
func (s *Server) handleAgentInternalQuestionWrite(writer http.ResponseWriter, request *http.Request, kind string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle, ok := s.requireAgentWorker(writer, request)
	if !ok {
		return
	}
	if !handle.Manager.WriteToolsEnabled() {
		writeAgentError(writer, agent.NewWriteDisabled())
		return
	}
	tools := handle.Manager.ToolsFor()
	if tools == nil {
		writeAgentError(writer, agent.NewInvalid("tools unavailable"))
		return
	}
	ctx := request.Context()
	switch kind {
	case "add":
		var body agent.AddQuestionCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if body.SourceDocumentID != "" && !looksLikeUUID(body.SourceDocumentID) {
			writeAgentError(writer, agent.NewInvalid("source_document_id must be a UUID"))
			return
		}
		result, err := tools.AddQuestion(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	case "answer":
		var body agent.AnswerQuestionCommand
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		result, err := tools.AnswerQuestion(ctx, body)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "result": result})
	case "delete":
		var body struct {
			Selector string `json:"selector"`
		}
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if err := tools.DeleteQuestion(ctx, strings.TrimSpace(body.Selector)); err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "deleted": true})
	default:
		writeAgentError(writer, agent.NewInvalid("unknown question tool"))
	}
}
