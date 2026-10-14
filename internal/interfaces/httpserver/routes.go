package httpserver

import "net/http"

// registerAuth wires the M3 login endpoints. Handlers answer 503 until the
// database is configured (EnableAuth), so development before M2 still works.
func (s *Server) registerAuth() {
	s.mux.HandleFunc("POST /v1/auth/otp/request", s.csrfGuard(s.handleOTPRequest))
	s.mux.HandleFunc("POST /v1/auth/otp/verify", s.csrfGuard(s.handleOTPVerify))
	s.mux.HandleFunc("POST /v1/auth/logout", s.csrfGuard(s.handleLogout))
	s.mux.HandleFunc("GET /v1/me", s.handleMe)
}

// registerDocuments wires the M4 document API. All routes require a session;
// mutations additionally pass the CSRF guard. Documents are 503 until
// EnableDocs runs.
func (s *Server) registerDocuments() {
	docs := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.docs == nil {
				writeError(w, http.StatusServiceUnavailable, "documents not configured")
				return
			}
			h(w, r)
		}
	}
	s.mux.HandleFunc("POST /v1/documents", s.csrfGuard(s.requireAuth(docs(s.handleCreateDocument))))
	s.mux.HandleFunc("GET /v1/documents", s.requireAuth(docs(s.handleListDocuments)))
	s.mux.HandleFunc("GET /v1/documents/{id}", s.requireAuth(docs(s.handleGetDocument)))
	s.mux.HandleFunc("GET /v1/documents/{id}/content", s.requireAuth(docs(s.handleDocumentContent)))
	s.mux.HandleFunc("PATCH /v1/documents/{id}", s.csrfGuard(s.requireAuth(docs(s.handlePatchDocument))))
	s.mux.HandleFunc("PUT /v1/documents/{id}/content", s.csrfGuard(s.requireAuth(docs(s.handleReplaceContent))))
	s.mux.HandleFunc("DELETE /v1/documents/{id}", s.csrfGuard(s.requireAuth(docs(s.handleDeleteDocument))))
}

// registerTopics wires the topics/issues API (000002 migration).
func (s *Server) registerTopics() {
	g := func(h http.HandlerFunc) http.HandlerFunc { return s.topicsGuard(h) }
	mut := func(h http.HandlerFunc) http.HandlerFunc { return s.csrfGuard(s.requireAuth(g(h))) }
	read := func(h http.HandlerFunc) http.HandlerFunc { return s.requireAuth(g(h)) }

	s.mux.HandleFunc("POST /v1/topics", mut(s.handleCreateTopic))
	s.mux.HandleFunc("GET /v1/topics", read(s.handleListTopics))
	s.mux.HandleFunc("PATCH /v1/topics/{id}", mut(s.handleUpdateTopic))
	s.mux.HandleFunc("DELETE /v1/topics/{id}", mut(s.handleDeleteTopic))
	s.mux.HandleFunc("GET /v1/topics/{id}/documents", read(s.handleTopicMembers))
	s.mux.HandleFunc("PUT /v1/topics/{id}/documents/{docID}", mut(s.handleAddMember))
	s.mux.HandleFunc("DELETE /v1/topics/{id}/documents/{docID}", mut(s.handleRemoveMember))
	s.mux.HandleFunc("GET /v1/topics/{id}/issues", read(s.handleListTopicIssues))
	s.mux.HandleFunc("PUT /v1/topics/{id}/issues/{issueID}", mut(s.handleLinkIssue))
	s.mux.HandleFunc("DELETE /v1/topics/{id}/issues/{issueID}", mut(s.handleUnlinkIssue))

	s.mux.HandleFunc("POST /v1/issues", mut(s.handleCreateIssue))
	s.mux.HandleFunc("GET /v1/issues", read(s.handleListIssues))
	s.mux.HandleFunc("PATCH /v1/issues/{id}", mut(s.handleUpdateIssue))
	s.mux.HandleFunc("DELETE /v1/issues/{id}", mut(s.handleDeleteIssue))
}
