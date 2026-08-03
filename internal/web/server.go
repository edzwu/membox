// Package web serves the embedded Miru markdown reader together with a small
// API that returns document bodies by selector. It powers `mm note view --web`:
// a local server renders any indexed Markdown file in the browser.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"membox/internal/application"
)

//go:embed all:static
var staticFS embed.FS

// Server is a localhost-only HTTP server exposing the Miru reader and a
// document API.
type Server struct {
	service    *application.Service
	httpServer *http.Server
	baseURL    string
}

func NewServer(service *application.Service) *Server {
	return &Server{service: service}
}

// Start binds to 127.0.0.1 on the given port (0 picks a free port), begins
// serving in the background, and returns the base URL.
func (s *Server) Start(ctx context.Context, port int) (string, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return "", fmt.Errorf("loading embedded web assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/doc/", s.handleDocument)
	mux.HandleFunc("/api/save", s.handleSave)
	mux.Handle("/", http.FileServer(http.FS(sub)))

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("starting web server: %w", err)
	}
	s.httpServer = &http.Server{Handler: mux}
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

func (s *Server) handleDocument(writer http.ResponseWriter, request *http.Request) {
	selector := strings.TrimPrefix(request.URL.Path, "/api/doc/")
	selector = strings.Trim(selector, "/")
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
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
	// Miru derives the displayed title from state.droppedFilename; send the
	// on-disk filename so the reader shows it instead of "Untitled".
	writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Membox-Filename", filepath.Base(document.Location.RelativePath))
	writer.Header().Set("X-Membox-Path", absolute)
	_, _ = writer.Write(body)
}

// saveRequest is the JSON body posted by the Miru "Save" button.
type saveRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// saveResponse reports the newly created document back to the browser.
type saveResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func (s *Server) handleSave(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload saveRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(writer, "invalid request body", http.StatusBadRequest)
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
		ID:   string(result.Document.ID),
		Path: result.Path,
	})
}
