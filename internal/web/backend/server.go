// Package backend implements the localhost HTTP API and serves a supplied
// frontend filesystem. It deliberately does not embed or own frontend assets;
// the parent web package composes the two at the application boundary.
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"membox/internal/application"
	"membox/internal/domain/catalog"
)

const integrationScript = `<script type="module" src="/membox/integration.js"></script>`

// Server is a localhost-only HTTP server exposing the membox API and Miru.
type Server struct {
	service       *application.Service
	miruFS        fs.FS
	integrationFS fs.FS
	httpServer    *http.Server
	baseURL       string
	token         string // empty = auth disabled (same-origin Miru / tests)
	annotationMu  sync.Mutex
}

// NewServer receives frontend files from the composition root rather than
// embedding them in the backend package.
func NewServer(service *application.Service, miruFS, integrationFS fs.FS) *Server {
	return &Server{service: service, miruFS: miruFS, integrationFS: integrationFS}
}

// SetToken enables bearer checks on extension-facing write endpoints.
// Empty token keeps those endpoints open (local Miru / unit tests).
func (s *Server) SetToken(token string) { s.token = strings.TrimSpace(token) }

// Token returns the configured bridge token, if any.
func (s *Server) Token() string { return s.token }

// BaseURL returns the listening URL after Start, or empty beforehand.
func (s *Server) BaseURL() string { return s.baseURL }

// Start binds to 127.0.0.1 on the given port (0 picks a free port), begins
// serving in the background, and returns the base URL.
func (s *Server) Start(ctx context.Context, port int) (string, error) {
	if _, err := fs.Stat(s.miruFS, "index.html"); err != nil {
		return "", fmt.Errorf("loading Miru frontend: %w", err)
	}
	// Upgrade old Extension-created *-note.md files before serving Miru. The
	// pass skips normalized rows, so subsequent starts only perform cheap index
	// lookups and also pick up old notes imported after the upgrade.
	if err := s.migrateExistingAnnotationNotes(ctx); err != nil {
		return "", fmt.Errorf("migrating annotation notes: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/bridge/status", s.handleBridgeStatus)
	mux.HandleFunc("/api/bridge/clips", s.handleBridgeClips)
	mux.HandleFunc("/api/ingest", s.handleIngest)
	mux.HandleFunc("/api/doc/", s.handleDocument)
	mux.HandleFunc("/api/save", s.handleSave)
	mux.HandleFunc("/api/sync", s.handleSync)
	mux.Handle("/membox/", noStore{http.StripPrefix("/membox/", http.FileServer(http.FS(s.integrationFS)))})
	mux.HandleFunc("/", s.handleFrontend)

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("starting web server: %w", err)
	}
	s.httpServer = &http.Server{Handler: withCORS(mux)}
	go func() { _ = s.httpServer.Serve(listener) }()

	s.baseURL = fmt.Sprintf("http://%s", listener.Addr().String())
	return s.baseURL, nil
}

// ViewURL returns the browser URL that renders the given document selector.
func (s *Server) ViewURL(selector string) string {
	return s.baseURL + "/?id=" + selector
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// noStore wraps a file handler so browsers never serve stale frontend assets:
// a cached old Miru/integration script against a new backend already cost us
// saved annotations once (stale restore logic overwrote a sidecar).
type noStore struct{ next http.Handler }

func (h noStore) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	h.next.ServeHTTP(writer, request)
}

// handleFrontend keeps the vendored Miru tree untouched. Only the served
// index response receives the membox adapter script; all other files are
// served byte-for-byte from frontend/miru.
func (s *Server) handleFrontend(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" && request.URL.Path != "/index.html" {
		writer.Header().Set("Cache-Control", "no-store")
		http.FileServer(http.FS(s.miruFS)).ServeHTTP(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := fs.ReadFile(s.miruFS, "index.html")
	if err != nil {
		http.Error(writer, "loading reader", http.StatusInternalServerError)
		return
	}
	page := strings.Replace(string(body), "</body>", "  "+integrationScript+"\n</body>", 1)
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodHead {
		return
	}
	_, _ = writer.Write([]byte(page))
}

func (s *Server) handleStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write([]byte(`{"connected":true}`))
}

func (s *Server) handleDocument(writer http.ResponseWriter, request *http.Request) {
	selector := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/doc/"), "/")
	if strings.HasSuffix(selector, "/annotations") {
		s.handleAnnotations(writer, request, strings.TrimSuffix(selector, "/annotations"))
		return
	}
	if strings.HasSuffix(selector, "/related") {
		s.handleRelated(writer, request, strings.TrimSuffix(selector, "/related"))
		return
	}
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	document, absolute, err := s.service.ResolveDocument(request.Context(), selector)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	body, err := s.service.ReadDocument(request.Context(), selector)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Membox-Filename", filepath.Base(document.Location.RelativePath))
	writer.Header().Set("X-Membox-Path", absolute)
	_, _ = writer.Write(body)
}

// handleRelated exposes the one-hop neighborhood of a document (GET) and
// creates a new plain Markdown document linked back to it (POST). The new
// document is a regular file, not a *-note.md selection note.
func (s *Server) handleRelated(writer http.ResponseWriter, request *http.Request, selector string) {
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	ctx := request.Context()
	switch request.Method {
	case http.MethodGet:
		focus, graph, err := s.service.GetDocumentGraph(ctx, selector)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusNotFound)
			return
		}
		type relatedView struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Path      string `json:"path"`
			Direction string `json:"direction"`
		}
		related := make([]relatedView, 0, len(graph.Outgoing)+len(graph.Incoming))
		appendLink := func(link catalog.DocumentLink, direction string) {
			if link.Document == nil {
				return
			}
			// Internal selection notes are part of the page's own annotation
			// surface, not related reading material.
			if strings.HasSuffix(link.Document.Location.RelativePath, "-note.md") {
				return
			}
			title := link.Document.Index.Title
			if strings.TrimSpace(title) == "" {
				title = path.Base(link.Document.Location.RelativePath)
			}
			related = append(related, relatedView{
				ID:        string(link.Document.ID),
				Title:     title,
				Path:      link.Document.Location.RelativePath,
				Direction: direction,
			})
		}
		for _, link := range graph.Outgoing {
			appendLink(link, "out")
		}
		for _, link := range graph.Incoming {
			appendLink(link, "in")
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"focus":   map[string]string{"id": string(focus.ID), "title": focus.Index.Title},
			"related": related,
		})
	case http.MethodPost, http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(request.Body, 8<<20))
		if err != nil {
			http.Error(writer, "reading related document body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var payload struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(writer, "invalid related document payload", http.StatusBadRequest)
			return
		}
		current, _, err := s.service.ResolveDocument(ctx, selector)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusNotFound)
			return
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			http.Error(writer, "related document title is required", http.StatusBadRequest)
			return
		}
		result, err := s.service.CreateNote(ctx, application.CreateNoteOptions{Title: title, Body: payload.Body})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		// The new document points back at the document it was created from, so
		// the source sees it as a backlink.
		if _, err := s.service.LinkDocuments(ctx, string(result.Document.ID), string(current.ID)); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id":       string(result.Document.ID),
			"title":    result.Document.Index.Title,
			"path":     result.Path,
			"view_url": s.ViewURL(string(result.Document.ID)),
		})
	default:
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAnnotations preserves Miru's portable sidecar wire format without
// making that JSON a second content store. GET assembles a transient view from
// Markdown note documents + normalized DB anchors; POST reconciles that view
// back to individual *-note.md files and stores reading progress separately.
func (s *Server) handleAnnotations(writer http.ResponseWriter, request *http.Request, selector string) {
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	switch request.Method {
	case http.MethodGet:
		sidecar, present, err := s.liveAnnotationSidecar(request.Context(), selector)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		if !present {
			// Compatibility for clients that explicitly saved an empty portable
			// envelope. It contains no note content; normalized notes never use it.
			if legacy, legacyErr := s.service.GetAnnotations(request.Context(), selector); legacyErr == nil && legacyEmptyAnnotationEnvelope(legacy) {
				_, _ = writer.Write([]byte(legacy))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(writer).Encode(sidecar)
	case http.MethodPost, http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(request.Body, 8<<20))
		if err != nil {
			http.Error(writer, "reading annotation body: "+err.Error(), http.StatusBadRequest)
			return
		}
		originalBody := string(body)
		clearing := strings.TrimSpace(originalBody) == ""
		if clearing {
			body = []byte(`{"format":"miru-annotations","version":2,"annotations":[],"replaceAnnotations":true}`)
		}
		var payload annotationWritePayload
		if err := json.Unmarshal(body, &payload); err != nil || payload.Format != "miru-annotations" || payload.Version != 2 {
			http.Error(writer, "invalid Miru annotation payload", http.StatusBadRequest)
			return
		}
		emptyEnvelope := !clearing && len(payload.Annotations) == 0 && payload.Progress == nil
		if clearing {
			// Zero progress with an empty timestamp deletes document_read_state.
			payload.Progress = &annotationProgressPayload{}
			// Server-authored clearing keeps full deletion authority.
			payload.Revision = math.MaxInt64
		}
		annotations, revision, err := s.reconcileAnnotationNotes(request.Context(), selector, payload)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		if emptyEnvelope {
			// Preserve only this content-free compatibility marker. Any real note
			// is always a Markdown document plus annotation_notes row.
			_, _ = s.service.SaveAnnotations(request.Context(), selector, originalBody)
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(writer).Encode(map[string]any{"saved": true, "annotations": annotations, "revision": revision})
	default:
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type saveRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type syncRequest struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Annotations json.RawMessage `json:"annotations"`
}

type saveResponse struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<20)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(target); err != nil {
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

func annotationPayload(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", fmt.Errorf("annotations must be a JSON object or null")
	}
	return trimmed, nil
}

// handleSync updates an existing source file when id is present, or creates a
// new indexed Markdown document otherwise. Its transient annotation payload is
// reconciled into separate *-note.md documents under the resulting UUID.
func (s *Server) handleSync(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload syncRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	if strings.TrimSpace(payload.Body) == "" {
		http.Error(writer, "Markdown body is required", http.StatusBadRequest)
		return
	}
	annotations, err := annotationPayload(payload.Annotations)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	response := saveResponse{}
	if strings.TrimSpace(payload.ID) == "" {
		result, createErr := s.service.CreateNote(request.Context(), application.CreateNoteOptions{
			Title: payload.Title,
			Body:  payload.Body,
		})
		if createErr != nil {
			http.Error(writer, createErr.Error(), http.StatusInternalServerError)
			return
		}
		response = saveResponse{ID: string(result.Document.ID), Path: result.Path, Created: true}
	} else {
		// The source Markdown and its selection-note documents have independent
		// authorities. Sync the source here, then reconcile annotation notes.
		result, syncErr := s.service.SyncDocument(request.Context(), payload.ID, payload.Body)
		if syncErr != nil {
			http.Error(writer, syncErr.Error(), http.StatusNotFound)
			return
		}
		response = saveResponse{ID: string(result.DocumentID), Path: result.Path, Created: false}
	}
	if annotations != "" {
		var annotationPayload annotationWritePayload
		if err := json.Unmarshal([]byte(annotations), &annotationPayload); err != nil || annotationPayload.Format != "miru-annotations" || annotationPayload.Version != 2 {
			http.Error(writer, "invalid annotation payload", http.StatusBadRequest)
			return
		}
		// Replacement authority stays with the client: it knows whether the
		// submitted set is complete (it merges unrestored anchors before sync).
		// Forcing true here turned any incomplete submission into a wipe.
		emptyEnvelope := len(annotationPayload.Annotations) == 0 && annotationPayload.Progress == nil
		if _, _, err := s.reconcileAnnotationNotes(request.Context(), response.ID, annotationPayload); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		if emptyEnvelope {
			_, _ = s.service.SaveAnnotations(request.Context(), response.ID, annotations)
		}
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(response)
}

func legacyEmptyAnnotationEnvelope(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var value struct {
		Format      string            `json:"format"`
		Version     int               `json:"version"`
		Annotations []json.RawMessage `json:"annotations"`
		Progress    json.RawMessage   `json:"progress"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil {
		return false
	}
	return value.Format == "miru-annotations" && value.Version == 2 && len(value.Annotations) == 0 && len(value.Progress) == 0
}

// handleSave is retained for API compatibility; new frontend code uses
// /api/sync so annotations and document identity travel in one request.
func (s *Server) handleSave(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload saveRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	result, err := s.service.CreateNote(request.Context(), application.CreateNoteOptions{
		Title: payload.Title,
		Body:  payload.Body,
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(saveResponse{
		ID:      string(result.Document.ID),
		Path:    result.Path,
		Created: true,
	})
}
