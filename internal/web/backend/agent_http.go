package backend

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"membox/internal/agent"
)

const (
	headerIdempotencyKey   = "Idempotency-Key"
	headerAgentClient      = "X-Membox-Agent-Client"
	headerCSRF             = "X-Membox-CSRF"
	agentAPIVersion        = 1
	sseHeartbeatInterval   = 15 * time.Second
)

// SetAgentManager wires the Companion-owned Agent control plane.
func (s *Server) SetAgentManager(m agent.Manager) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	s.agent = m
}

func (s *Server) agentManager() agent.Manager {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	return s.agent
}

func (s *Server) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/agent/status", s.handleAgentStatus)
	mux.HandleFunc("/api/agent/sessions", s.handleAgentSessions)
	mux.HandleFunc("/api/agent/sessions/", s.handleAgentSessionSubroutes)
	mux.HandleFunc("/api/agent/models", s.handleAgentModels)
	// Internal worker-only endpoints (no CORS, bearer+worker binding).
	mux.HandleFunc("/api/agent/internal/context", s.handleAgentInternalContext)
	mux.HandleFunc("/api/agent/internal/tools/search", s.handleAgentInternalSearch)
	mux.HandleFunc("/api/agent/internal/tools/read", s.handleAgentInternalRead)
	mux.HandleFunc("/api/agent/internal/tools/get", s.handleAgentInternalGet)
	mux.HandleFunc("/api/agent/internal/tools/related", s.handleAgentInternalRelated)
	mux.HandleFunc("/api/agent/internal/tools/create_note", s.handleAgentInternalWriteDisabled)
	mux.HandleFunc("/api/agent/internal/tools/update", s.handleAgentInternalWriteDisabled)
	mux.HandleFunc("/api/agent/internal/tools/rename", s.handleAgentInternalWriteDisabled)
}

func (s *Server) handleAgentStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowAgentPublic(writer, request, false) {
		return
	}
	mgr := s.agentManager()
	if mgr == nil {
		writeAgentJSON(writer, http.StatusOK, map[string]any{
			"v": agentAPIVersion, "enabled": false, "available": false, "state": "disabled",
			"loaded_workers": 0, "max_workers": 0, "error": "agent manager not configured",
		})
		return
	}
	status, err := mgr.Status(request.Context())
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{
		"v":               agentAPIVersion,
		"enabled":         status.Enabled,
		"available":       status.Available,
		"pi_version":      status.PiVersion,
		"state":           status.State,
		"loaded_workers":  status.LoadedWorkers,
		"max_workers":     status.MaxWorkers,
		"write_tools":     status.WriteTools,
		"error":           status.Error,
	})
}

func (s *Server) handleAgentSessions(writer http.ResponseWriter, request *http.Request) {
	mgr := s.agentManager()
	if mgr == nil {
		writeAgentError(writer, agent.NewDisabled())
		return
	}
	switch request.Method {
	case http.MethodGet:
		if !s.allowAgentPublic(writer, request, false) {
			return
		}
		includeArchived := request.URL.Query().Get("include_archived") == "true"
		list, err := mgr.ListSessions(request.Context(), agent.ListSessionsQuery{IncludeArchived: includeArchived})
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "sessions": list})
	case http.MethodPost:
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		var body struct {
			Title         string          `json:"title"`
			Model         *agent.ModelRef `json:"model"`
			ThinkingLevel string          `json:"thinking_level"`
		}
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		snap, err := mgr.CreateSession(request.Context(), agent.CreateSessionCommand{
			Title:          body.Title,
			Model:          body.Model,
			ThinkingLevel:  body.ThinkingLevel,
			IdempotencyKey: request.Header.Get(headerIdempotencyKey),
			ClientID:       request.Header.Get(headerAgentClient),
		})
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusCreated, snapshotResponse(snap))
	default:
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentSessionSubroutes(writer http.ResponseWriter, request *http.Request) {
	mgr := s.agentManager()
	if mgr == nil {
		writeAgentError(writer, agent.NewDisabled())
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/agent/sessions/")
	parts := splitPath(path)
	if len(parts) == 0 {
		http.NotFound(writer, request)
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 {
		s.handleAgentSessionRoot(writer, request, mgr, sessionID)
		return
	}
	switch parts[1] {
	case "archive":
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		if err := mgr.ArchiveSession(request.Context(), sessionID); err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "archived": true})
	case "messages":
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		var body struct {
			Text    string `json:"text"`
			Context *struct {
				DocumentID string `json:"document_id"`
			} `json:"context"`
		}
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		docID := ""
		if body.Context != nil {
			docID = body.Context.DocumentID
		}
		run, err := mgr.Prompt(request.Context(), agent.PromptCommand{
			SessionID:      sessionID,
			ClientID:       request.Header.Get(headerAgentClient),
			IdempotencyKey: request.Header.Get(headerIdempotencyKey),
			Text:           body.Text,
			DocumentID:     docID,
		})
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusAccepted, map[string]any{
			"v": agentAPIVersion, "run_id": run.RunID, "accepted": run.Accepted,
		})
	case "events":
		if request.Method != http.MethodGet {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.allowAgentPublic(writer, request, false) {
			return
		}
		after := request.Header.Get("Last-Event-ID")
		if after == "" {
			after = request.URL.Query().Get("after")
		}
		s.serveAgentSSE(writer, request, mgr, sessionID, after)
	case "model":
		if request.Method != http.MethodPatch {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		var body agent.ModelRef
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if err := mgr.SetModel(request.Context(), sessionID, body); err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "ok": true})
	case "thinking":
		if request.Method != http.MethodPatch {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		var body struct {
			Level string `json:"level"`
		}
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		if err := mgr.SetThinking(request.Context(), sessionID, body.Level); err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "ok": true})
	case "runs":
		if len(parts) < 3 {
			http.NotFound(writer, request)
			return
		}
		runID := parts[2]
		if len(parts) == 4 && parts[3] == "abort" {
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !s.allowAgentPublic(writer, request, true) {
				return
			}
			if err := mgr.Abort(request.Context(), sessionID, runID, request.Header.Get(headerAgentClient)); err != nil {
				writeAgentError(writer, err)
				return
			}
			writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "aborted": true})
			return
		}
		if len(parts) == 4 && parts[3] == "claim" {
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !s.allowAgentPublic(writer, request, true) {
				return
			}
			if err := mgr.Claim(request.Context(), sessionID, runID, request.Header.Get(headerAgentClient)); err != nil {
				writeAgentError(writer, err)
				return
			}
			writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "claimed": true})
			return
		}
		if len(parts) == 5 && parts[3] == "approvals" {
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !s.allowAgentPublic(writer, request, true) {
				return
			}
			var body struct {
				Decision string `json:"decision"`
			}
			if err := decodeJSONBody(request, &body); err != nil {
				writeAgentError(writer, agent.NewInvalid(err.Error()))
				return
			}
			if err := mgr.ResolveApproval(request.Context(), agent.ResolveApprovalCommand{
				SessionID:  sessionID,
				RunID:      runID,
				ApprovalID: parts[4],
				ClientID:   request.Header.Get(headerAgentClient),
				Decision:   body.Decision,
			}); err != nil {
				writeAgentError(writer, err)
				return
			}
			writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "ok": true})
			return
		}
		http.NotFound(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleAgentSessionRoot(writer http.ResponseWriter, request *http.Request, mgr agent.Manager, sessionID string) {
	switch request.Method {
	case http.MethodGet:
		if !s.allowAgentPublic(writer, request, false) {
			return
		}
		snap, err := mgr.Snapshot(request.Context(), sessionID)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, snapshotResponse(snap))
	case http.MethodPatch:
		if !s.allowAgentPublic(writer, request, true) {
			return
		}
		var body struct {
			Title string `json:"title"`
		}
		if err := decodeJSONBody(request, &body); err != nil {
			writeAgentError(writer, agent.NewInvalid(err.Error()))
			return
		}
		view, err := mgr.RenameSession(request.Context(), sessionID, body.Title)
		if err != nil {
			writeAgentError(writer, err)
			return
		}
		writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "session": view})
	default:
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentModels(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowAgentPublic(writer, request, false) {
		return
	}
	mgr := s.agentManager()
	if mgr == nil {
		writeAgentError(writer, agent.NewDisabled())
		return
	}
	models, err := mgr.ListModels(request.Context())
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"v": agentAPIVersion, "models": models})
}

func (s *Server) serveAgentSSE(writer http.ResponseWriter, request *http.Request, mgr agent.Manager, sessionID, after string) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, err := mgr.Subscribe(request.Context(), sessionID, after)
	if err != nil {
		writeAgentError(writer, err)
		return
	}
	defer sub.Close()

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			_, _ = io.WriteString(writer, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			payload, _ := json.Marshal(ev)
			_, _ = io.WriteString(writer, "id: "+ev.ID+"\n")
			_, _ = io.WriteString(writer, "event: agent\n")
			_, _ = io.WriteString(writer, "data: "+string(payload)+"\n\n")
			flusher.Flush()
		}
	}
}

// allowAgentPublic enforces localhost-only, optional bearer for non-browser
// clients, and CSRF/origin checks for mutating browser requests.
// Agent API deliberately does not participate in extension CORS.
func (s *Server) allowAgentPublic(writer http.ResponseWriter, request *http.Request, mutating bool) bool {
	// Reject cross-site browser navigations.
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return false
	}
	origin := request.Header.Get("Origin")
	if origin != "" && !sameOrigin(origin, s.baseURL) && !extensionOrigin(origin) {
		// Extension origins are not granted Agent API access.
		if extensionOrigin(origin) {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return false
		}
		// Non-matching origins rejected.
		if !strings.HasPrefix(origin, "http://127.0.0.1") && !strings.HasPrefix(origin, "http://localhost") {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return false
		}
	}
	if mutating {
		// Browser mutations need CSRF header OR companion bearer token.
		hasCSRF := request.Header.Get(headerCSRF) == "1"
		hasBearer := s.token != "" && bearerToken(request) == s.token
		hasClient := strings.TrimSpace(request.Header.Get(headerAgentClient)) != ""
		if !hasBearer && !(hasCSRF && hasClient) {
			// Allow bearer-less local tests when token is empty, still require client id.
			if s.token == "" && hasClient {
				return true
			}
			if hasBearer && hasClient {
				return true
			}
			http.Error(writer, `{"error":{"code":"unauthorized","message":"missing csrf or bearer"}}`, http.StatusUnauthorized)
			return false
		}
		if !hasClient {
			http.Error(writer, `{"error":{"code":"invalid_request","message":"X-Membox-Agent-Client required"}}`, http.StatusBadRequest)
			return false
		}
	} else if s.token != "" {
		// Non-mutating: browser same-origin is enough; external clients need bearer.
		// EventSource cannot set Authorization; same-origin cookie/no-cors local is OK.
		// If Authorization present, it must match.
		if auth := bearerToken(request); auth != "" && auth != s.token {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return false
		}
	}
	return true
}

func sameOrigin(origin, baseURL string) bool {
	if origin == "" || baseURL == "" {
		return false
	}
	return strings.TrimRight(origin, "/") == strings.TrimRight(baseURL, "/")
}

func bearerToken(request *http.Request) string {
	h := request.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func snapshotResponse(snap agent.SessionSnapshot) map[string]any {
	return map[string]any{
		"v":             agentAPIVersion,
		"session":       snap.Session,
		"messages":      snap.Messages,
		"active_run":    snap.ActiveRun,
		"last_event_id": snap.LastEventID,
		"stream_epoch":  snap.StreamEpoch,
	}
}

func writeAgentJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeAgentError(writer http.ResponseWriter, err error) {
	code := agent.CodeOf(err)
	status := http.StatusInternalServerError
	switch code {
	case agent.CodeDisabled, agent.CodeUnavailable, agent.CodeIncompatiblePi, agent.CodeNotAuthenticated:
		status = http.StatusServiceUnavailable
	case agent.CodeBusy:
		status = http.StatusConflict
	case agent.CodeCapacityReached:
		status = http.StatusTooManyRequests
	case agent.CodeSessionNotFound, agent.CodeSessionFileMissing:
		status = http.StatusNotFound
	case agent.CodeNotRunController:
		status = http.StatusForbidden
	case agent.CodeApprovalExpired, agent.CodeApprovalResolved, agent.CodeRevisionConflict:
		status = http.StatusConflict
	case agent.CodeInvalidRequest, agent.CodeMutationDenied, agent.CodeWriteToolsDisabled:
		status = http.StatusBadRequest
	case agent.CodeWorkerStartFailed, agent.CodeWorkerExited, agent.CodeProtocolError:
		status = http.StatusBadGateway
	}
	msg := err.Error()
	writeAgentJSON(writer, status, map[string]any{
		"v": agentAPIVersion,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}

func decodeJSONBody(request *http.Request, dst any) error {
	defer request.Body.Close()
	dec := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
