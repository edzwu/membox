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
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"membox/internal/agent"
	"membox/internal/application"
	"membox/internal/domain/catalog"
	"membox/internal/pdfasset"
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
	// documentMu serializes source reads/mutations with annotation restore/save.
	// The title is editable while restoreReadingState is still resolving paths,
	// so rename must not split that operation between the old and new location.
	documentMu   sync.Mutex
	annotationMu sync.Mutex

	// Web Companion control plane: lifecycle mode, connected browser tabs, and
	// the callbacks that stop the process or change modes at runtime.
	companionMu          sync.Mutex
	companionMode        string
	companionStartedAt   time.Time
	companionTabs        map[string]tabPresence
	companionLeases      map[string]time.Time
	companionEverLeased  bool
	onCompanionStop      func()
	onCompanionLifecycle func(mode string)

	// Agent control plane (Pi RPC workers). Optional; nil outside Companion.
	agentMu sync.Mutex
	agent   agent.Manager
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
	mux.HandleFunc("/api/companion/status", s.handleCompanionStatus)
	mux.HandleFunc("/api/companion/stop", s.handleCompanionStop)
	mux.HandleFunc("/api/companion/lifecycle", s.handleCompanionLifecycle)
	mux.HandleFunc("/api/companion/lease", s.handleCompanionLease)
	mux.HandleFunc("/api/companion/presence", s.handleCompanionPresence)
	mux.HandleFunc("/api/bridge/status", s.handleBridgeStatus)
	mux.HandleFunc("/api/bridge/clips", s.handleBridgeClips)
	mux.HandleFunc("/api/ingest", s.handleIngest)
	mux.HandleFunc("/api/resources", s.handleResources)
	mux.HandleFunc("/api/resources/ingest", s.handleResourceIngest)
	mux.HandleFunc("/api/resources/assess", s.handleResourceAssess)
	mux.HandleFunc("/api/resources/scan", s.handleResourceScan)
	mux.HandleFunc("/api/documents/candidates", s.handleDocumentCandidates)
	mux.HandleFunc("/api/doc/", s.handleDocument)
	mux.HandleFunc("/api/pdfs/import", s.handlePDFImport)
	mux.HandleFunc("/api/pdf-assets/", s.handlePDFAsset)
	mux.HandleFunc("/api/save", s.handleSave)
	mux.HandleFunc("/api/sync", s.handleSync)
	s.registerAgentRoutes(mux)
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
	if mgr := s.agentManager(); mgr != nil {
		_ = mgr.Shutdown(ctx)
	}
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
	if strings.HasSuffix(selector, "/related/candidates") {
		s.handleRelatedCandidates(writer, request, strings.TrimSuffix(selector, "/related/candidates"))
		return
	}
	if strings.HasSuffix(selector, "/rename") {
		s.handleDocumentRename(writer, request, strings.TrimSuffix(selector, "/rename"))
		return
	}
	if strings.HasSuffix(selector, "/annotations") {
		s.handleAnnotations(writer, request, strings.TrimSuffix(selector, "/annotations"))
		return
	}
	if strings.HasSuffix(selector, "/read-status") {
		s.handleReadStatus(writer, request, strings.TrimSuffix(selector, "/read-status"))
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
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
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
	// Opening a document is reading activity even when the user does not scroll.
	// Recency is best-effort and must never prevent the Markdown from loading.
	_ = s.service.MarkDocumentOpened(request.Context(), selector)
	writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	// HTTP header values do not have a browser-portable Unicode encoding.
	// Keep the custom header ASCII-only and let the frontend decode UTF-8.
	writer.Header().Set("X-Membox-Filename", url.PathEscape(filepath.Base(document.Location.RelativePath)))
	// Display title from the catalog (front matter / H1), not just the basename.
	// The reader prefers this so a rename that updated body title survives reopen.
	title := strings.TrimSpace(document.Index.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(document.Location.RelativePath), filepath.Ext(document.Location.RelativePath))
	}
	writer.Header().Set("X-Membox-Title", url.PathEscape(title))
	writer.Header().Set("X-Membox-Path", absolute)
	_, _ = writer.Write(body)
}

func (s *Server) handlePDFAsset(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	remainder := strings.TrimPrefix(request.URL.Path, "/api/pdf-assets/")
	separator := strings.IndexByte(remainder, '/')
	if separator <= 0 || separator == len(remainder)-1 {
		http.Error(writer, "invalid PDF asset path", http.StatusBadRequest)
		return
	}
	selector, relativePath := remainder[:separator], remainder[separator+1:]
	document, pdfPath, err := s.service.ResolveDocument(request.Context(), selector)
	if err != nil || document.Status != catalog.DocumentActive || document.Index.MediaType != "application/pdf" {
		http.Error(writer, "PDF not found", http.StatusNotFound)
		return
	}
	assetRoot, err := pdfasset.Root(pdfPath, string(document.ID))
	if err != nil {
		http.Error(writer, "invalid PDF asset root", http.StatusBadRequest)
		return
	}
	target, err := pdfasset.ImageTarget(assetRoot, relativePath)
	if err != nil {
		http.Error(writer, "invalid PDF asset path", http.StatusBadRequest)
		return
	}
	realRoot, err := filepath.EvalSymlinks(assetRoot)
	if err != nil {
		http.Error(writer, "PDF asset not found", http.StatusNotFound)
		return
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil || !pathWithin(realRoot, realTarget) {
		http.Error(writer, "PDF asset not found", http.StatusNotFound)
		return
	}
	file, err := os.Open(realTarget)
	if err != nil {
		http.Error(writer, "PDF asset not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(writer, "PDF asset not found", http.StatusNotFound)
		return
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(realTarget)))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", mediaType)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.EqualFold(filepath.Ext(realTarget), ".svg") {
		writer.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	http.ServeContent(writer, request, filepath.Base(realTarget), info.ModTime(), file)
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (s *Server) handleDocumentCandidates(writer http.ResponseWriter, request *http.Request) {
	s.handleRelatedCandidates(writer, request, strings.TrimSpace(request.URL.Query().Get("focus")))
}

func (s *Server) handleDocumentRename(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if selector == "" {
		http.Error(writer, "document selector is required", http.StatusBadRequest)
		return
	}
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	var payload struct {
		Filename string `json:"filename"`
		Title    string `json:"title"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	result, err := s.service.RenameDocument(request.Context(), selector, payload.Filename, payload.Title)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"id":       string(result.DocumentID),
		"path":     result.Path,
		"filename": filepath.Base(result.Path),
		"title":    result.Title,
	})
}

// handleRelatedCandidates powers both document switching and the
// existing-related picker. An empty query returns recently opened documents
// first, then fills the list from all membox documents by newest modification.
// A non-empty query matches literal UUID/title/path fragments. The open picker
// normally omits its focus and internal selection notes, but an explicit UUID
// fragment can find either; related mode always omits existing graph neighbors.
func (s *Server) handleRelatedCandidates(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	queryLower := strings.ToLower(query)
	purpose := strings.TrimSpace(request.URL.Query().Get("purpose"))
	if purpose != "open" && selector == "" {
		http.Error(writer, "document selector is required", http.StatusBadRequest)
		return
	}
	limit := 8
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			http.Error(writer, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	excluded := map[string]bool{}
	if selector != "" {
		if purpose == "open" {
			focus, _, err := s.service.ResolveDocument(request.Context(), selector)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusNotFound)
				return
			}
			focusID := string(focus.ID)
			// Keep the current document out of the ordinary switcher list, but
			// let an explicit UUID fragment find it. This makes pasting the ID a
			// reliable identity lookup rather than an apparently empty search.
			if queryLower == "" || !strings.Contains(strings.ToLower(focusID), queryLower) {
				excluded[focusID] = true
			}
		} else {
			focus, graph, err := s.service.GetDocumentGraph(request.Context(), selector)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusNotFound)
				return
			}
			excluded[string(focus.ID)] = true
			for _, link := range append(graph.Outgoing, graph.Incoming...) {
				if link.Document != nil {
					excluded[string(link.Document.ID)] = true
				}
			}
		}
	}
	fetchLimit := limit * 4
	if fetchLimit > 100 {
		fetchLimit = 100
	}
	type candidateView struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Path     string `json:"path"`
		OpenedAt string `json:"opened_at,omitempty"`
	}
	candidates := make([]candidateView, 0, limit)
	included := make(map[string]bool, limit)
	appendCandidate := func(id, title, documentPath, openedAt string) bool {
		if excluded[id] || included[id] {
			return false
		}
		// Selection-note documents stay out of normal title/recent browsing,
		// but their durable UUID is still a valid Ctrl+O identity. Include one
		// when the entered fragment actually occurs in that UUID.
		if strings.HasSuffix(documentPath, "-note.md") &&
			(purpose != "open" || queryLower == "" || !strings.Contains(strings.ToLower(id), queryLower)) {
			return false
		}
		title = strings.TrimSpace(title)
		if title == "" {
			title = path.Base(documentPath)
		}
		candidates = append(candidates, candidateView{ID: id, Title: title, Path: documentPath, OpenedAt: openedAt})
		included[id] = true
		return len(candidates) == limit
	}
	if query == "" {
		recent, recentErr := s.service.ListRecentDocuments(request.Context(), fetchLimit)
		if recentErr != nil {
			http.Error(writer, recentErr.Error(), http.StatusInternalServerError)
			return
		}
		for _, document := range recent {
			if appendCandidate(string(document.DocumentID), document.Title, document.Path, document.OpenedAt) {
				break
			}
		}
		if len(candidates) < limit {
			modified, modifiedErr := s.service.ListRecentlyModifiedDocuments(request.Context(), fetchLimit)
			if modifiedErr != nil {
				http.Error(writer, modifiedErr.Error(), http.StatusInternalServerError)
				return
			}
			for _, document := range modified {
				if appendCandidate(string(document.DocumentID), document.Title, document.Path, "") {
					break
				}
			}
		}
	} else {
		hits, suggestErr := s.service.SuggestDocuments(request.Context(), query, fetchLimit)
		if suggestErr != nil {
			http.Error(writer, suggestErr.Error(), http.StatusBadRequest)
			return
		}
		for _, hit := range hits {
			if appendCandidate(string(hit.DocumentID), hit.Title, hit.Path, "") {
				break
			}
		}
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"candidates": candidates})
}

// handleRelated exposes the one-hop neighborhood of a document (GET), links
// an existing document, or creates a new plain Markdown document linked back
// to it. New documents are regular files, not *-note.md selection notes.
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
			ID            string `json:"id"`
			Title         string `json:"title"`
			Path          string `json:"path"`
			Direction     string `json:"direction"`
			AnnotationRef string `json:"annotation_ref,omitempty"`
		}
		// When the focus itself is a selection-note document, identify its
		// source relation. The reader uses this durable note UUID to return to
		// the exact passage instead of merely opening the source at its saved
		// scroll position.
		annotation, isAnnotation, err := s.service.GetAnnotationNote(ctx, string(focus.ID))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
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
			item := relatedView{
				ID:        string(link.Document.ID),
				Title:     title,
				Path:      link.Document.Location.RelativePath,
				Direction: direction,
			}
			if isAnnotation && link.Document.ID == annotation.TargetDocumentID {
				item.AnnotationRef = string(annotation.NoteDocumentID)
			}
			related = append(related, item)
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
			TargetID string `json:"target_id"`
			Title    string `json:"title"`
			Body     string `json:"body"`
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
		if targetID := strings.TrimSpace(payload.TargetID); targetID != "" {
			target, _, resolveErr := s.service.ResolveDocument(ctx, targetID)
			if resolveErr != nil {
				http.Error(writer, resolveErr.Error(), http.StatusNotFound)
				return
			}
			if target.ID == current.ID {
				http.Error(writer, "a document cannot be related to itself", http.StatusBadRequest)
				return
			}
			if strings.HasSuffix(target.Location.RelativePath, "-note.md") {
				http.Error(writer, "selection notes cannot be added as related documents", http.StatusBadRequest)
				return
			}
			linked, linkErr := s.service.LinkDocuments(ctx, string(current.ID), string(target.ID))
			if linkErr != nil {
				http.Error(writer, linkErr.Error(), http.StatusInternalServerError)
				return
			}
			title := strings.TrimSpace(target.Index.Title)
			if title == "" {
				title = path.Base(target.Location.RelativePath)
			}
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":             string(target.ID),
				"title":          title,
				"path":           target.Location.RelativePath,
				"already_exists": linked.AlreadyExists,
				"view_url":       s.ViewURL(string(target.ID)),
			})
			return
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			http.Error(writer, "related document title is required", http.StatusBadRequest)
			return
		}
		result, err := s.service.CreateNote(ctx, application.CreateNoteOptions{
			Title: title, Body: payload.Body, AlignBodyTitle: true,
		})
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

// handleReadStatus records the semantic reading state (unread/reading/
// finished) reported by the reader UI: opening marks reading, scrolling to
// the end marks finished.
type readStatusPayload struct {
	Status string `json:"status"`
}

func (s *Server) handleReadStatus(writer http.ResponseWriter, request *http.Request, selector string) {
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload readStatusPayload
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(writer, "invalid read-status payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.service.SetDocumentReadStatus(request.Context(), selector, payload.Status); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": payload.Status})
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
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
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
		annotations, revision, replacementApplied, err := s.reconcileAnnotationNotes(request.Context(), selector, payload)
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
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"saved": true, "annotations": annotations, "revision": revision,
			"replacement_applied": replacementApplied,
		})
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
	ID                 string           `json:"id"`
	Path               string           `json:"path"`
	Created            bool             `json:"created"`
	Annotations        []map[string]any `json:"annotations,omitempty"`
	Revision           int64            `json:"revision,omitempty"`
	ReplacementApplied *bool            `json:"replacement_applied,omitempty"`
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
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
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
		saved, revision, replacementApplied, reconcileErr := s.reconcileAnnotationNotes(request.Context(), response.ID, annotationPayload)
		if reconcileErr != nil {
			http.Error(writer, reconcileErr.Error(), http.StatusInternalServerError)
			return
		}
		response.Annotations = saved
		response.Revision = revision
		response.ReplacementApplied = &replacementApplied
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
