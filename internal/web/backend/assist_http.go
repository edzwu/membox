package backend

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"membox/internal/assist"
)

// assistRunner is the Pi one-shot runner for Miru selection assist.
// Set via SetAssistRunner from the companion composition root.
var (
	assistMu     sync.RWMutex
	assistRunner *assist.Runner
)

// SetAssistRunner wires the selection-assist model runner (typically deepseek-v4-flash).
func (s *Server) SetAssistRunner(runner *assist.Runner) {
	assistMu.Lock()
	defer assistMu.Unlock()
	assistRunner = runner
}

func getAssistRunner() *assist.Runner {
	assistMu.RLock()
	defer assistMu.RUnlock()
	if assistRunner != nil {
		return assistRunner
	}
	// Default: deepseek-v4-flash via Pi on PATH, auth from user home.
	home, _ := os.UserHomeDir()
	return &assist.Runner{
		PiPath:   "pi",
		Home:     home,
		Provider: assist.DefaultProvider,
		Model:    assist.DefaultModel,
	}
}

// handleSelectionSummarize summarizes a user-selected excerpt with the local
// mmd model and returns the ≤140-char summary. The frontend saves it as an
// annotation note (kind=summary) via the normal note flow.
func (s *Server) handleSelectionSummarize(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if _, _, err := s.service.ResolveDocument(request.Context(), selector); err != nil {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "reading summarize request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Selection string `json:"selection"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(writer, "invalid summarize payload", http.StatusBadRequest)
		return
	}
	selection := strings.TrimSpace(payload.Selection)
	if selection == "" {
		http.Error(writer, "selection is empty", http.StatusBadRequest)
		return
	}
	if len([]rune(selection)) > 4000 {
		selection = string([]rune(selection)[:4000])
	}
	if s.summarizer == nil {
		http.Error(writer, "local model (mmd) is not configured", http.StatusServiceUnavailable)
		return
	}
	prompt := "你是笔记整理助手。把下面选中的内容压缩成一段 140 字以内的中文总结，保留核心观点，直接输出总结本身，不要前缀不要解释。\n\n选中内容：\n" + selection + "\n\n140字以内的总结："
	summary, err := s.summarizer.Complete(request.Context(), prompt)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusConflict)
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		http.Error(writer, "local model returned an empty summary", http.StatusConflict)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"summary": summary,
		"model":   "local qwen3:14b",
	})
}

func (s *Server) handleDocumentAssist(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	// Ensure the document exists (assist is scoped to an open membox doc).
	if _, _, err := s.service.ResolveDocument(request.Context(), selector); err != nil {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "reading assist request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload assist.Request
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(writer, "invalid assist payload", http.StatusBadRequest)
		return
	}
	req, err := assist.Validate(payload)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	runner := getAssistRunner()
	writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	flusher, _ := writer.(http.Flusher)
	emit := func(event assist.Event) error {
		if err := json.NewEncoder(writer).Encode(event); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	if err := runner.Stream(request.Context(), req, emit); err != nil {
		// Error event may already have been emitted; if stream never started,
		// the client still sees the connection close after partial NDJSON.
		return
	}
}

// handleAssistApply applies a replacement to the document body and syncs it,
// creating a new content version via SyncDocument.
func (s *Server) handleAssistApply(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 8<<20))
	if err != nil {
		http.Error(writer, "reading apply request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Selection   string `json:"selection"`
		Replacement string `json:"replacement"`
		Prefix      string `json:"prefix"`
		Suffix      string `json:"suffix"`
		// Optional full body from the client (preferred when the editor has
		// unsaved local edits that differ from disk).
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(writer, "invalid apply payload", http.StatusBadRequest)
		return
	}
	s.documentMu.Lock()
	defer s.documentMu.Unlock()

	markdown := payload.Body
	if strings.TrimSpace(markdown) == "" {
		raw, readErr := s.service.ReadDocument(request.Context(), selector)
		if readErr != nil {
			http.Error(writer, readErr.Error(), http.StatusNotFound)
			return
		}
		markdown = string(raw)
	}
	next, err := assist.ApplyReplacement(markdown, payload.Selection, payload.Replacement, payload.Prefix, payload.Suffix)
	if err != nil {
		// The browser sends rendered text (no ** / ` / [](), headings/list
		// markers stripped), which cannot be found verbatim in the Markdown
		// source. Fall back to canonical→source mapping so the user's selection
		// is never lost between the reader and the file.
		if start, end, ok := LocateRenderedSelection(markdown, payload.Selection); ok {
			mdRunes := []rune(markdown)
			if start >= 0 && end <= len(mdRunes) && start < end {
				next = string(mdRunes[:start]) + payload.Replacement + string(mdRunes[end:])
				err = nil
			}
		}
		if err != nil {
			http.Error(writer, err.Error(), http.StatusConflict)
			return
		}
	}
	result, err := s.service.SyncDocument(request.Context(), selector, next)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"id":       string(result.DocumentID),
		"path":     result.Path,
		"filename": filepath.Base(result.Path),
		"body":     next,
	})
}
