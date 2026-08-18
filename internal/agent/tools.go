package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"membox/internal/application"
	"membox/internal/domain/catalog"
)

// Default body chunk size returned by read tool (characters, not bytes).
const defaultReadLimit = 12000

// DocumentTools is the application-port surface used by the membox Pi extension
// through internal HTTP endpoints. Write methods exist for Phase 4 but are
// gated by Manager.WriteToolsEnabled.
type DocumentTools interface {
	SearchDocuments(ctx context.Context, query string, limit int) ([]DocumentHit, error)
	GrepDocuments(ctx context.Context, pattern string, limit int) ([]DocumentHit, error)
	ReadDocument(ctx context.Context, id string, cursor string, limit int) (DocumentChunk, error)
	GetDocument(ctx context.Context, id string) (DocumentView, error)
	ListRelated(ctx context.Context, id string) (RelatedView, error)
	// Question accumulation tools.
	ListQuestions(ctx context.Context, status string, limit int) ([]QuestionHit, error)
	// Write tools (Phase 4) — registered only when write tools are enabled.
	CreateNote(ctx context.Context, cmd CreateNoteCommand) (MutationResult, error)
	UpdateDocument(ctx context.Context, cmd UpdateDocumentCommand) (MutationResult, error)
	RenameDocument(ctx context.Context, cmd RenameDocumentCommand) (MutationResult, error)
	LinkDocuments(ctx context.Context, cmd LinkDocumentsCommand) (MutationResult, error)
	AddQuestion(ctx context.Context, cmd AddQuestionCommand) (QuestionResult, error)
	AnswerQuestion(ctx context.Context, cmd AnswerQuestionCommand) (QuestionResult, error)
	DeleteQuestion(ctx context.Context, selector string) error
}

// DocumentHit is one search result.
type DocumentHit struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Path    string `json:"path,omitempty"`
}

// DocumentChunk is a bounded body read.
type DocumentChunk struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Revision   string `json:"revision"`
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	NextCursor string `json:"next_cursor,omitempty"`
	TotalRunes int    `json:"total_runes"`
	Truncated  bool   `json:"truncated"`
}

// DocumentView is metadata for one document.
type DocumentView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Revision  string `json:"revision"`
	Summary   string `json:"summary,omitempty"`
	MediaType string `json:"media_type"`
	Authors   string `json:"authors,omitempty"`
	Year      int    `json:"year,omitempty"`
	Keywords  string `json:"keywords,omitempty"`
	PageCount int    `json:"page_count,omitempty"`
	Pinned    bool   `json:"pinned"`
	Path      string `json:"path,omitempty"`
}

// QuestionHit is one question record for agent tool results.
type QuestionHit struct {
	ID               string `json:"id"`
	Body             string `json:"body"`
	Status           string `json:"status"`
	Answer           string `json:"answer,omitempty"`
	SourceDocumentID string `json:"source_document_id,omitempty"`
}

// AddQuestionCommand records a new question (agent-facing).
type AddQuestionCommand struct {
	Body             string `json:"body"`
	SourceDocumentID string `json:"source_document_id,omitempty"`
}

// AnswerQuestionCommand answers or archives a question.
type AnswerQuestionCommand struct {
	Selector string `json:"selector"`
	Answer   string `json:"answer,omitempty"`
	Status   string `json:"status,omitempty"` // answered | archived
}

// QuestionResult reports a question mutation.
type QuestionResult struct {
	ID      string `json:"id"`
	Body    string `json:"body,omitempty"`
	Status  string `json:"status"`
	Answer  string `json:"answer,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// RelatedView summarizes neighborhood links.
type RelatedView struct {
	ID     string        `json:"id"`
	Notes  []DocumentHit `json:"notes"`
	Links  []DocumentHit `json:"links"`
	Topics []DocumentHit `json:"topics"`
}

// CreateNoteCommand creates a plain note (Phase 4).
type CreateNoteCommand struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	TargetID  string `json:"target_id,omitempty"`
	PreviewOK bool   `json:"-"`
}

// UpdateDocumentCommand replaces document body with revision check (Phase 4).
type UpdateDocumentCommand struct {
	ID               string `json:"id"`
	ExpectedRevision string `json:"expected_revision"`
	Body             string `json:"body"`
}

// RenameDocumentCommand renames while preserving UUID (Phase 4).
type RenameDocumentCommand struct {
	ID               string `json:"id"`
	ExpectedRevision string `json:"expected_revision"`
	NewFilename      string `json:"new_filename"`
}

// LinkDocumentsCommand connects two documents by UUID, preserving identity of
// both sides (Phase 4, used by wiki synthesis to keep Raw → Card traceability).
type LinkDocumentsCommand struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
}

// MutationResult is returned by write tools.
type MutationResult struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Revision string `json:"revision,omitempty"`
	Denied   bool   `json:"denied,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ServiceDocumentTools adapts application.Service for agent tools.
type ServiceDocumentTools struct {
	Service *application.Service
}

func (t *ServiceDocumentTools) SearchDocuments(ctx context.Context, query string, limit int) ([]DocumentHit, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	hits, err := t.Service.Search(ctx, query, limit, false)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, DocumentHit{
			ID:      string(hit.DocumentID),
			Title:   hit.Title,
			Snippet: hit.Snippet,
			Path:    hit.Path,
		})
	}
	return out, nil
}

// GrepDocuments is literal substring search (rg -i semantics) for rare
// keywords and code identifiers that FTS tokenization would miss.
func (t *ServiceDocumentTools) GrepDocuments(ctx context.Context, pattern string, limit int) ([]DocumentHit, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	hits, err := t.Service.Grep(ctx, pattern, limit)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, DocumentHit{
			ID:      string(hit.DocumentID),
			Title:   hit.Title,
			Snippet: hit.Snippet,
			Path:    hit.Path,
		})
	}
	return out, nil
}

func (t *ServiceDocumentTools) ListQuestions(ctx context.Context, status string, limit int) ([]QuestionHit, error) {
	questions, err := t.Service.ListQuestions(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]QuestionHit, 0, len(questions))
	for _, q := range questions {
		out = append(out, QuestionHit{
			ID: q.ID, Body: q.Body, Status: q.Status, Answer: q.Answer,
			SourceDocumentID: q.SourceDocumentID,
		})
	}
	return out, nil
}

func (t *ServiceDocumentTools) AddQuestion(ctx context.Context, cmd AddQuestionCommand) (QuestionResult, error) {
	question, err := t.Service.AddQuestion(ctx, application.AddQuestionCommand{
		Body:             cmd.Body,
		SourceDocumentID: cmd.SourceDocumentID,
	})
	if err != nil {
		return QuestionResult{}, err
	}
	return QuestionResult{ID: question.ID, Body: question.Body, Status: question.Status}, nil
}

func (t *ServiceDocumentTools) AnswerQuestion(ctx context.Context, cmd AnswerQuestionCommand) (QuestionResult, error) {
	question, err := t.Service.AnswerQuestion(ctx, application.AnswerQuestionCommand{
		Selector: cmd.Selector,
		Answer:   cmd.Answer,
		Status:   cmd.Status,
	})
	if err != nil {
		return QuestionResult{}, err
	}
	return QuestionResult{ID: question.ID, Body: question.Body, Status: question.Status, Answer: question.Answer}, nil
}

func (t *ServiceDocumentTools) DeleteQuestion(ctx context.Context, selector string) error {
	return t.Service.DeleteQuestion(ctx, selector)
}

func (t *ServiceDocumentTools) GetDocument(ctx context.Context, id string) (DocumentView, error) {
	doc, abs, err := t.Service.ResolveDocument(ctx, id)
	if err != nil {
		return DocumentView{}, err
	}
	return documentView(doc, abs), nil
}

func (t *ServiceDocumentTools) ReadDocument(ctx context.Context, id string, cursor string, limit int) (DocumentChunk, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if limit > 40000 {
		limit = 40000
	}
	doc, _, err := t.Service.ResolveDocument(ctx, id)
	if err != nil {
		return DocumentChunk{}, err
	}
	body, err := t.Service.ReadDocumentText(ctx, id)
	if err != nil {
		return DocumentChunk{}, err
	}
	text := string(body)
	total := utf8.RuneCountInString(text)
	offset := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &offset); err != nil || offset < 0 {
			return DocumentChunk{}, fmtError(CodeInvalidRequest, "invalid read cursor")
		}
	}
	runes := []rune(text)
	if offset > len(runes) {
		offset = len(runes)
	}
	end := offset + limit
	if end > len(runes) {
		end = len(runes)
	}
	chunk := string(runes[offset:end])
	next := ""
	truncated := end < len(runes)
	if truncated {
		next = fmt.Sprintf("%d", end)
	}
	return DocumentChunk{
		ID:         string(doc.ID),
		Title:      doc.Index.Title,
		Revision:   documentRevision(doc),
		Text:       chunk,
		Offset:     offset,
		NextCursor: next,
		TotalRunes: total,
		Truncated:  truncated,
	}, nil
}

func (t *ServiceDocumentTools) ListRelated(ctx context.Context, id string) (RelatedView, error) {
	doc, graph, err := t.Service.GetDocumentGraph(ctx, id)
	if err != nil {
		return RelatedView{}, err
	}
	view := RelatedView{ID: string(doc.ID)}
	for _, link := range graph.Outgoing {
		if link.Document == nil {
			continue
		}
		view.Links = append(view.Links, DocumentHit{ID: string(link.Document.ID), Title: link.Document.Index.Title})
	}
	for _, link := range graph.Incoming {
		if link.Document == nil {
			continue
		}
		view.Links = append(view.Links, DocumentHit{ID: string(link.Document.ID), Title: link.Document.Index.Title})
	}
	for _, topic := range graph.Topics {
		if topic.Document == nil {
			continue
		}
		view.Topics = append(view.Topics, DocumentHit{ID: string(topic.Document.ID), Title: topic.Document.Index.Title})
	}
	// Also list annotation notes via dedicated API.
	if _, notes, noteErr := t.Service.ListAnnotationNotes(ctx, id); noteErr == nil {
		seen := map[string]bool{}
		for _, n := range view.Notes {
			seen[n.ID] = true
		}
		for _, note := range notes {
			nid := string(note.NoteDocumentID)
			if seen[nid] {
				continue
			}
			if noteDoc, _, rerr := t.Service.ResolveDocument(ctx, nid); rerr == nil {
				view.Notes = append(view.Notes, DocumentHit{ID: nid, Title: noteDoc.Index.Title})
			} else {
				view.Notes = append(view.Notes, DocumentHit{ID: nid})
			}
		}
	}
	return view, nil
}

func (t *ServiceDocumentTools) CreateNote(ctx context.Context, cmd CreateNoteCommand) (MutationResult, error) {
	title := strings.TrimSpace(cmd.Title)
	if title == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "note title is required")
	}
	if strings.TrimSpace(cmd.Body) == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "note body is required")
	}
	result, err := t.Service.CreateNote(ctx, application.CreateNoteOptions{
		Title:        title,
		Body:         cmd.Body,
		FromSelector: strings.TrimSpace(cmd.TargetID),
	})
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		ID:       string(result.Document.ID),
		Title:    result.Document.Index.Title,
		Revision: documentRevision(result.Document),
	}, nil
}

func (t *ServiceDocumentTools) UpdateDocument(ctx context.Context, cmd UpdateDocumentCommand) (MutationResult, error) {
	if strings.TrimSpace(cmd.ID) == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "document id is required")
	}
	if strings.TrimSpace(cmd.Body) == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "body is required")
	}
	current, _, err := t.Service.ResolveDocument(ctx, cmd.ID)
	if err != nil {
		return MutationResult{}, err
	}
	currentRev := documentRevision(current)
	if strings.TrimSpace(cmd.ExpectedRevision) != "" && cmd.ExpectedRevision != currentRev {
		return MutationResult{}, fmtError(CodeRevisionConflict, "revision conflict: expected %s, current %s", cmd.ExpectedRevision, currentRev)
	}
	result, err := t.Service.SyncDocument(ctx, cmd.ID, cmd.Body)
	if err != nil {
		return MutationResult{}, err
	}
	doc, _, err := t.Service.ResolveDocument(ctx, cmd.ID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		ID:       string(result.DocumentID),
		Title:    doc.Index.Title,
		Revision: documentRevision(doc),
	}, nil
}

func (t *ServiceDocumentTools) RenameDocument(ctx context.Context, cmd RenameDocumentCommand) (MutationResult, error) {
	if strings.TrimSpace(cmd.ID) == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "document id is required")
	}
	current, _, err := t.Service.ResolveDocument(ctx, cmd.ID)
	if err != nil {
		return MutationResult{}, err
	}
	currentRev := documentRevision(current)
	if strings.TrimSpace(cmd.ExpectedRevision) != "" && cmd.ExpectedRevision != currentRev {
		return MutationResult{}, fmtError(CodeRevisionConflict, "revision conflict: expected %s, current %s", cmd.ExpectedRevision, currentRev)
	}
	result, err := t.Service.RenameDocument(ctx, cmd.ID, cmd.NewFilename, "")
	if err != nil {
		return MutationResult{}, err
	}
	doc, _, err := t.Service.ResolveDocument(ctx, cmd.ID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		ID:       string(result.DocumentID),
		Title:    doc.Index.Title,
		Revision: documentRevision(doc),
	}, nil
}

func (t *ServiceDocumentTools) LinkDocuments(ctx context.Context, cmd LinkDocumentsCommand) (MutationResult, error) {
	from := strings.TrimSpace(cmd.FromID)
	to := strings.TrimSpace(cmd.ToID)
	if from == "" || to == "" {
		return MutationResult{}, fmtError(CodeInvalidRequest, "from_id and to_id are required")
	}
	if from == to {
		return MutationResult{}, fmtError(CodeInvalidRequest, "a document cannot link to itself")
	}
	_, err := t.Service.LinkDocuments(ctx, from, to)
	if err != nil {
		return MutationResult{}, err
	}
	toDoc, _, err := t.Service.ResolveDocument(ctx, to)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		ID:    to,
		Title: toDoc.Index.Title,
	}, nil
}

func documentView(doc *catalog.Document, abs string) DocumentView {
	return DocumentView{
		ID:        string(doc.ID),
		Title:     doc.Index.Title,
		Status:    string(doc.Status),
		Revision:  documentRevision(doc),
		Summary:   doc.Index.Summary,
		MediaType: doc.Index.MediaType,
		Authors:   doc.Index.Authors,
		Year:      doc.Index.Year,
		Keywords:  doc.Index.Keywords,
		PageCount: doc.Index.PageCount,
		Pinned:    doc.Pinned,
		Path:      abs,
	}
}

// documentRevision is a stable content identity the agent can round-trip.
func documentRevision(doc *catalog.Document) string {
	if doc.Index.SHA256 != "" {
		return doc.Index.SHA256
	}
	return fmt.Sprintf("mtime:%d:size:%d", doc.Index.MTime, doc.Index.Size)
}

// ToolNamesReadOnly is the V1 allowlist passed to pi --tools.
func ToolNamesReadOnly() []string {
	return []string{
		"membox_search_documents",
		"membox_grep_documents",
		"membox_read_document",
		"membox_list_related",
		"membox_get_document",
		"membox_list_questions",
	}
}

// ToolNamesAll includes write tools (Phase 4).
func ToolNamesAll() []string {
	return append(ToolNamesReadOnly(),
		"membox_create_note",
		"membox_update_document",
		"membox_rename_document",
		"membox_link_documents",
		"membox_add_question",
		"membox_answer_question",
		"membox_delete_question",
	)
}

// MarshalToolResult is a helper for extension-facing JSON payloads.
func MarshalToolResult(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"error":"marshal_failed"}`)
	}
	return raw
}
