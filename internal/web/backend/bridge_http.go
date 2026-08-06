package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application"
)

const bridgeTokenHeader = "X-Membox-Token"

// BridgeFile is written next to the database so the browser extension can pair.
// Mode and PID describe the Web Companion that owns the port (extension clients
// ignore unknown fields).
type BridgeFile struct {
	BaseURL     string `json:"base_url"`
	Token       string `json:"token"`
	Port        int    `json:"port"`
	Mode        string `json:"mode,omitempty"`
	PID         int    `json:"pid,omitempty"`
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
func BridgeFilePath(home string) string { return filepath.Join(home, "bridge.json") }

// BridgeTokenPath is the durable pairing secret. Keeping it separate from
// volatile runtime state means a torn/corrupt bridge.json never rotates the
// browser extension's long-lived token.
func BridgeTokenPath(home string) string { return filepath.Join(home, "bridge.token") }

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

// LoadOrCreateBridgeToken loads the durable token, migrates the legacy token
// from bridge.json, or creates one atomically.
func LoadOrCreateBridgeToken(home string) (string, error) {
	if body, err := os.ReadFile(BridgeTokenPath(home)); err == nil {
		if token := strings.TrimSpace(string(body)); token != "" {
			return token, nil
		}
	}
	var token string
	if existing, err := ReadBridgeFile(home); err == nil {
		token = strings.TrimSpace(existing.Token)
	}
	if token == "" {
		var err error
		token, err = NewBridgeToken()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := writePrivateFileAtomic(BridgeTokenPath(home), []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

// WriteBridgeFile persists pairing info atomically under membox home.
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
	if err := writePrivateFileAtomic(path, append(body, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

func writePrivateFileAtomic(destination string, body []byte) error {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".membox-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	// Persist the directory entry on filesystems that support directory fsync.
	if directoryFile, err := os.Open(directory); err == nil {
		_ = directoryFile.Sync()
		_ = directoryFile.Close()
	}
	return nil
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

type bridgeClip struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt,omitempty"`
	Note    string `json:"note,omitempty"`
	Mode    string `json:"clip_mode,omitempty"`
}

// handleBridgeClips lists selection notes registered for a source_url in
// document_sources (written at ingest time). Optional mode=all includes page clips.
func (s *Server) handleBridgeClips(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	sourceURL := normalizeSourceURL(request.URL.Query().Get("source_url"))
	if sourceURL == "" {
		http.Error(writer, "source_url is required", http.StatusBadRequest)
		return
	}
	selectionOnly := strings.TrimSpace(request.URL.Query().Get("mode")) != "all"
	ctx := request.Context()
	// Opportunistically wire page → selection edges for older clips that only
	// had source_url text and never got a graph_edges row.
	s.repairSourceLinks(ctx, sourceURL)
	clips := s.listClipsBySourceURL(ctx, sourceURL, selectionOnly)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"source_url": sourceURL,
		"count":      len(clips),
		"clips":      clips,
	})
}

// repairSourceLinks ensures every selection note for URL is linked from the
// page clip (manual graph edge). Safe to call repeatedly.
func (s *Server) repairSourceLinks(ctx context.Context, sourceURL string) {
	pageID := s.findPageDocumentID(ctx, sourceURL)
	if pageID == "" {
		return
	}
	for _, sel := range s.listClipsBySourceURL(ctx, sourceURL, true) {
		if sel.ID == pageID {
			continue
		}
		_, _ = s.service.LinkDocuments(ctx, pageID, sel.ID)
	}
}

func (s *Server) listClipsBySourceURL(ctx context.Context, sourceURL string, selectionNotesOnly bool) []bridgeClip {
	norm := normalizeSourceURL(sourceURL)
	if norm == "" {
		return nil
	}
	records, err := s.service.ListClipsBySourceURL(ctx, norm, selectionNotesOnly)
	if err != nil || len(records) == 0 {
		return nil
	}
	out := make([]bridgeClip, 0, len(records))
	for _, rec := range records {
		excerpt, note := "", ""
		if body, readErr := s.service.ReadDocument(ctx, string(rec.DocumentID)); readErr == nil {
			excerpt, note, _ = parseClipBody(string(body))
		}
		title := strings.TrimSpace(rec.Title)
		if title == "" {
			title = string(rec.DocumentID)
		}
		out = append(out, bridgeClip{
			ID:      string(rec.DocumentID),
			Title:   title,
			Excerpt: excerpt,
			Note:    note,
			Mode:    rec.ClipMode,
		})
	}
	return out
}

// normalizeSourceURL strips fragments and noisy tracking query params so the
// same article matches across share links (WeChat adds scene/token params).
func normalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	host := u.Host
	// WeChat / many share pages: path id is stable; query is not.
	if strings.Contains(host, "weixin.qq.com") ||
		(strings.Contains(host, "qq.com") && strings.HasPrefix(u.Path, "/s")) {
		u.RawQuery = ""
	} else {
		q := u.Query()
		for _, key := range []string{
			"from", "scene", "sessionid", "ascene", "devicetype", "version",
			"nettype", "abtest_cookie", "wx_header", "poc_token", "srcid",
			"sharer_shareinfo", "sharer_shareinfo_first", "mpshare", "clicktime",
			"enterid", "exportkey", "pass_ticket", "uin", "key", "wxtoken",
		} {
			q.Del(key)
		}
		u.RawQuery = q.Encode()
	}
	u.Path = path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if u.Path != "/" {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

func sourceURLSearchKeys(raw string) []string {
	norm := normalizeSourceURL(raw)
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		// Skip tiny tokens and scheme-only noise.
		if len(k) < 6 || k == "https" || k == "http" {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	// Prefer distinctive short keys first (better FTS hits).
	if u, err := url.Parse(norm); err == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		// weixin: /s/<articleId>
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "s" && len(parts[i+1]) >= 8 {
				add(parts[i+1])
			}
		}
		if len(parts) > 0 {
			add(parts[len(parts)-1])
		}
		add(strings.Trim(u.Path, "/"))
		add(u.Host)
	}
	add(norm)
	add(raw)
	return keys
}

func bodyCitesSourceURL(body, sourceURL string) bool {
	for _, key := range sourceURLSearchKeys(sourceURL) {
		if strings.Contains(body, key) {
			return true
		}
	}
	// Front-matter source_url may use the normalized form.
	if fm, ok := frontMatterSourceURL(body); ok {
		if normalizeSourceURL(fm) == normalizeSourceURL(sourceURL) {
			return true
		}
		for _, key := range sourceURLSearchKeys(sourceURL) {
			if strings.Contains(fm, key) {
				return true
			}
		}
	}
	return false
}

func frontMatterSourceURL(body string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "---") {
		return "", false
	}
	rest := strings.TrimSpace(body)[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "source_url:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "source_url:"))
		val = strings.Trim(val, `"'`)
		if val != "" {
			return val, true
		}
	}
	return "", false
}

func parseClipBody(body string) (excerpt, note, mode string) {
	// Front matter clip_mode
	if i := strings.Index(body, "clip_mode:"); i >= 0 {
		line := body[i:]
		if end := strings.IndexByte(line, '\n'); end > 0 {
			mode = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line[:end], "clip_mode:")), `"' `)
		}
	}
	// Body after front matter
	content := body
	if strings.HasPrefix(content, "---") {
		rest := content[3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			content = strings.TrimSpace(rest[end+4:])
		}
	}
	// Selection notes: blockquote excerpt then note paragraphs.
	var excerptLines, noteLines []string
	inExcerpt := true
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, ">") {
			inExcerpt = true
			// Unquote one blockquote level but keep the line's own indentation:
			// ">     list.Sort()" must stay "    list.Sort()" so multi-line
			// excerpts match the rendered article text across saves.
			unquoted := strings.TrimPrefix(strings.TrimLeft(line, " \t"), ">")
			unquoted = strings.TrimPrefix(unquoted, " ")
			excerptLines = append(excerptLines, strings.TrimRight(unquoted, " \t\r"))
			continue
		}
		if trim == "" {
			if inExcerpt {
				if len(excerptLines) > 0 {
					inExcerpt = false
				}
				continue
			}
			// Blank lines inside the note body are meaningful (multiline notes).
			if len(noteLines) > 0 {
				noteLines = append(noteLines, "")
			}
			continue
		}
		if strings.HasPrefix(trim, "Source:") {
			break
		}
		if inExcerpt && len(excerptLines) == 0 {
			// full-page clip: use first non-empty as excerpt preview
			excerptLines = append(excerptLines, strings.TrimLeft(trim, "# "))
			inExcerpt = false
			continue
		}
		if trim == "_No note._" {
			continue
		}
		noteLines = append(noteLines, trim)
	}
	excerpt = strings.TrimSpace(strings.Join(excerptLines, "\n"))
	note = strings.TrimSpace(strings.Join(noteLines, "\n"))
	return excerpt, note, mode
}

type ingestRequest struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SourceURL string `json:"source_url"`
	ClipMode  string `json:"clip_mode"` // selection | page
	// Raw selection text (pre-Markdown), preferred for annotation anchoring.
	ExcerptRaw string `json:"excerpt_raw"`
	// From is an optional existing document selector. When set (or when a page
	// clip with the same source_url already exists), the new note is graph-linked
	// from that document via CreateNote's FromSelector.
	From string `json:"from"`
}

type ingestResponse struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
	ViewURL string `json:"view_url"`
	Linked  string `json:"linked,omitempty"`
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
	sourceURL := normalizeSourceURL(payload.SourceURL)
	clipMode := detectClipMode(body, payload)
	// If the client sent a bare body without front matter, attach source meta.
	if sourceURL != "" && !strings.HasPrefix(body, "---") {
		body = assembleClipMarkdown(title, sourceURL, body)
	}

	ctx := request.Context()
	// Resolve graph parent up front for selection notes (page clip of same URL).
	pageID := strings.TrimSpace(payload.From)
	if pageID == "" && sourceURL != "" && clipMode == "selection" {
		pageID = s.findPageDocumentID(ctx, sourceURL)
	}

	result, err := s.service.CreateNote(ctx, application.CreateNoteOptions{
		Title:        title,
		Body:         body,
		FromSelector: pageID, // page → selection edge when parent exists
		SourceURL:    sourceURL,
		ClipMode:     clipMode,
	})
	// Never fail ingest because linking failed — retry without From, then link.
	if err != nil && pageID != "" {
		result, err = s.service.CreateNote(ctx, application.CreateNoteOptions{
			Title:     title,
			Body:      body,
			SourceURL: sourceURL,
			ClipMode:  clipMode,
		})
		pageID = s.findPageDocumentID(ctx, sourceURL) // may still exist
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	id := string(result.Document.ID)

	linked := ""
	if clipMode == "selection" && sourceURL != "" {
		// Direct graph link: page document → this selection note.
		if pageID == "" {
			pageID = s.findPageDocumentID(ctx, sourceURL)
		}
		if pageID != "" && pageID != id {
			if _, linkErr := s.service.LinkDocuments(ctx, pageID, id); linkErr == nil {
				linked = pageID
			} else if result.Link != nil {
				linked = pageID
			}
		}
		if linked == "" && result.Link != nil && pageID != "" {
			linked = pageID
		}
		// Store the normalized note UUID + anchor relation. Miru derives its
		// transient rendering DTO from this relation and the note Markdown.
		if pageID != "" && pageID != id {
			excerpt, _, _ := parseClipBody(body)
			if strings.TrimSpace(payload.ExcerptRaw) != "" {
				excerpt = payload.ExcerptRaw
			}
			_, _ = s.upsertClipAnnotationRelation(ctx, pageID, id, excerpt)
		}
	}
	if clipMode == "page" && sourceURL != "" {
		// Page save: attach every existing selection note for this URL.
		for _, sel := range s.listClipsBySourceURL(ctx, sourceURL, true) {
			if sel.ID == id {
				continue
			}
			if _, linkErr := s.service.LinkDocuments(ctx, id, sel.ID); linkErr == nil {
				linked = id // mark that linking ran from this page
			}
		}
		// Notes saved before this page clip arrived get their UUID/anchor
		// relation once the target document exists.
		s.backfillPageAnnotations(ctx, id, sourceURL)
	}

	resp := ingestResponse{
		ID:      id,
		Path:    result.Path,
		Created: true,
		ViewURL: s.ViewURL(id),
		Linked:  linked,
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(resp)
}

// findPageDocumentID returns the full-page clip registered for this URL in
// document_sources (clip_mode=page). Selection notes are never used as parents.
func (s *Server) findPageDocumentID(ctx context.Context, sourceURL string) string {
	for _, clip := range s.listClipsBySourceURL(ctx, sourceURL, false) {
		if clip.Mode == "page" {
			return clip.ID
		}
	}
	return ""
}

func detectClipMode(body string, payload ingestRequest) string {
	if mode := strings.TrimSpace(payload.ClipMode); mode == "selection" || mode == "page" {
		return mode
	}
	if _, _, mode := parseClipBody(body); mode == "selection" || mode == "page" {
		return mode
	}
	if strings.Contains(body, "clip_mode: \"selection\"") || strings.Contains(body, "clip_mode: selection") {
		return "selection"
	}
	title := strings.TrimSpace(payload.Title)
	if strings.HasSuffix(title, "— note") || strings.HasSuffix(title, "- note") {
		return "selection"
	}
	if payload.SourceURL != "" {
		return "page"
	}
	return ""
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
