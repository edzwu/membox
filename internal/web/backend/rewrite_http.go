package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"membox/internal/docrewrite"
	"membox/internal/domain/catalog"
)

// Layout-polish rewrites are serialized for the same reason as document
// summaries: the local mmd model handles one completion at a time, so
// parallel runs would just queue inside mmd.
var docRewriteMu sync.Mutex

var (
	errRewriteBusy     = errors.New("another document rewrite is running")
	errRewriteNotFound = errors.New("document not found")
	errRewritePDF      = errors.New("document is a PDF; convert to Markdown first")
)

// defaultPolishHint constrains the generic rewrite brief to layout work:
// heading hierarchy, list indentation, blank lines. Body sentences must
// survive verbatim — the Miru preview diff verifies exactly that.
const defaultPolishHint = `本次只做排版优化，不改写内容：
1. 理顺标题层级：同级小节用同级标题，嵌套关系正确，不要跳级（如 H2 直接到 H4）。
2. 修列表缩进、空行与明显断裂的行；代码围栏的语言标记保持或修正为正确语言。
3. 正文逐字保留：不增删句子，不改写措辞，不增删链接与图片。
4. 拿不准的地方保持原样，宁少勿改。`

// rewriteFunc is the seam between the HTTP boundary and the LLM pipeline.
// Production wires docrewrite.Runner (mmd local / Pi cloud); tests inject a
// fake so no model is needed.
type rewriteFunc func(ctx context.Context, home, title, markdown, model, hint string, onProgress func(docrewrite.Progress)) (docrewrite.Result, error)

// runDocumentRewrite is replaceable in tests.
var runDocumentRewrite rewriteFunc = defaultDocumentRewrite

func defaultDocumentRewrite(ctx context.Context, home, title, markdown, model, hint string, onProgress func(docrewrite.Progress)) (docrewrite.Result, error) {
	spec, err := docrewrite.ParseModel(model)
	if err != nil {
		return docrewrite.Result{}, err
	}
	runner := docrewrite.Runner{Home: home, OnProgress: onProgress}
	return runner.Rewrite(ctx, docrewrite.Request{
		Title: title, Markdown: markdown, Model: spec, Hint: hint,
	})
}

// handleDocumentRewrite rewrites the document's Markdown for readability and
// streams progress as NDJSON (long documents take minutes). The result is
// returned for Miru's preview/confirm flow — this endpoint never writes the
// file; saving stays with /api/sync after the user confirms.
func (s *Server) handleDocumentRewrite(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if s.home == "" {
		http.Error(writer, "membox home is not configured", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Model string `json:"model"`
		Hint  string `json:"hint"`
	}
	if request.Body != nil {
		// Empty body is fine; the local model is the default.
		_ = json.NewDecoder(request.Body).Decode(&payload)
	}

	// Only resolve+read under the document lock — the rewrite itself can run
	// for minutes and must not block other document reads.
	s.documentMu.Lock()
	document, _, resolveErr := s.service.ResolveDocument(request.Context(), selector)
	var body []byte
	if resolveErr == nil {
		if document == nil || document.Status != catalog.DocumentActive {
			resolveErr = errRewriteNotFound
		} else if strings.TrimSpace(document.Index.MediaType) == "application/pdf" {
			resolveErr = errRewritePDF
		} else {
			body, resolveErr = s.service.ReadDocument(request.Context(), string(document.ID))
		}
	}
	s.documentMu.Unlock()
	if resolveErr != nil {
		http.Error(writer, resolveErr.Error(), rewriteErrorStatus(resolveErr))
		return
	}

	if !docRewriteMu.TryLock() {
		http.Error(writer, errRewriteBusy.Error(), http.StatusConflict)
		return
	}
	defer docRewriteMu.Unlock()

	writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	started := false
	emit := func(event map[string]any) {
		started = true
		if err := json.NewEncoder(writer).Encode(event); err != nil {
			return
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	hint := defaultPolishHint
	if extra := strings.TrimSpace(payload.Hint); extra != "" {
		hint += "\n" + extra
	}
	result, err := runDocumentRewrite(
		request.Context(), s.home, strings.TrimSpace(document.Index.Title), string(body), payload.Model, hint,
		func(p docrewrite.Progress) {
			emit(map[string]any{
				"type":   "progress",
				"done":   p.Done,
				"total":  p.Total,
				"detail": p.Detail,
			})
		},
	)
	if err != nil {
		if !started {
			http.Error(writer, err.Error(), rewriteErrorStatus(err))
			return
		}
		emit(map[string]any{"type": "error", "text": err.Error()})
		return
	}
	emit(map[string]any{
		"type":       "done",
		"markdown":   result.Markdown,
		"label":      result.Label,
		"provider":   result.Provider,
		"model":      result.Model,
		"chunks":     result.Chunks,
		"chunkRunes": result.ChunkRunes,
	})
}

func rewriteErrorStatus(err error) int {
	switch {
	case errors.Is(err, errRewriteNotFound):
		return http.StatusNotFound
	case errors.Is(err, errRewritePDF):
		return http.StatusBadRequest
	case strings.Contains(err.Error(), "invalid document selector"):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "unknown model"), strings.Contains(err.Error(), "invalid model"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
