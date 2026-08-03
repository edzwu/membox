package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application"
)

const bridgeTokenHeader = "X-Membox-Token"

// BridgeFile is written next to the database so the browser extension can pair.
type BridgeFile struct {
	BaseURL     string `json:"base_url"`
	Token       string `json:"token"`
	Port        int    `json:"port"`
	HostVersion string `json:"host_version,omitempty"`
	WrittenAt   string `json:"written_at"`
}

// NewBridgeToken returns a random 32-byte hex token.
func NewBridgeToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// BridgeFilePath is $MEMBOX_HOME/bridge.json.
func BridgeFilePath(home string) string {
	return filepath.Join(home, "bridge.json")
}

// ReadBridgeFile loads an existing pairing file, if any.
func ReadBridgeFile(home string) (BridgeFile, error) {
	body, err := os.ReadFile(BridgeFilePath(home))
	if err != nil {
		return BridgeFile{}, err
	}
	var file BridgeFile
	if err := json.Unmarshal(body, &file); err != nil {
		return BridgeFile{}, err
	}
	return file, nil
}

// LoadOrCreateBridgeToken reuses the token from bridge.json when present so
// browser extensions stay paired across TUI / serve restarts.
func LoadOrCreateBridgeToken(home string) (string, error) {
	if existing, err := ReadBridgeFile(home); err == nil {
		if token := strings.TrimSpace(existing.Token); token != "" {
			return token, nil
		}
	}
	return NewBridgeToken()
}

// WriteBridgeFile persists pairing info under membox home.
func WriteBridgeFile(home string, file BridgeFile) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("membox home is required")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if file.WrittenAt == "" {
		file.WrittenAt = time.Now().UTC().Format(time.RFC3339)
	}
	path := BridgeFilePath(home)
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if extensionOrigin(origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+bridgeTokenHeader)
			writer.Header().Set("Access-Control-Max-Age", "86400")
			writer.Header().Set("Vary", "Origin")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func extensionOrigin(origin string) bool {
	return strings.HasPrefix(origin, "chrome-extension://") ||
		strings.HasPrefix(origin, "moz-extension://")
}

func (s *Server) requireBridgeToken(writer http.ResponseWriter, request *http.Request) bool {
	if s.token == "" {
		return true
	}
	got := strings.TrimSpace(request.Header.Get(bridgeTokenHeader))
	if got == "" || got != s.token {
		http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handleBridgeStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"connected":     true,
		"auth_required": s.token != "",
	})
}

type ingestRequest struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SourceURL string `json:"source_url"`
}

type ingestResponse struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
	ViewURL string `json:"view_url"`
}

// handleIngest creates a new indexed Markdown note from a browser clip and
// returns its stable UUID plus a Miru view URL.
func (s *Server) handleIngest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	var payload ingestRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	title := strings.TrimSpace(payload.Title)
	body := strings.TrimSpace(payload.Body)
	if body == "" {
		http.Error(writer, "Markdown body is required", http.StatusBadRequest)
		return
	}
	if title == "" {
		title = titleFromBody(body)
	}
	if title == "" {
		title = "Clipped page"
	}
	// If the client sent a bare body without front matter, attach source meta.
	if payload.SourceURL != "" && !strings.HasPrefix(body, "---") {
		body = assembleClipMarkdown(title, payload.SourceURL, body)
	}

	result, err := s.service.CreateNote(request.Context(), application.CreateNoteOptions{
		Title: title,
		Body:  body,
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	id := string(result.Document.ID)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(ingestResponse{
		ID:      id,
		Path:    result.Path,
		Created: true,
		ViewURL: s.ViewURL(id),
	})
}

func titleFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func assembleClipMarkdown(title, sourceURL, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + strconvQuote(title) + "\n")
	if sourceURL != "" {
		b.WriteString("source_url: " + strconvQuote(sourceURL) + "\n")
	}
	b.WriteString("clipped_at: " + strconvQuote(time.Now().UTC().Format(time.RFC3339)) + "\n")
	b.WriteString("clipper: membox-clipper\n")
	b.WriteString("---\n\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func strconvQuote(value string) string {
	// Minimal YAML double-quoted scalar.
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
}
