package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Service) ListDocuments(ctx context.Context, limit int, includeUnavailable bool, statusFilter string) ([]port.DocumentRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 5000 {
		return nil, errors.New("document list limit cannot exceed 5000")
	}
	return s.store.ListDocuments(ctx, limit, includeUnavailable, statusFilter)
}

// SetDocumentReadStatus records the semantic reading state (unread/reading/
// finished) for a document. Opening marks reading; scrolling to the end marks
// finished; the TUI cycles the state with tab.
func (s *Service) SetDocumentReadStatus(ctx context.Context, selector, status string) error {
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return err
	}
	if status != "unread" && status != "reading" && status != "finished" {
		return fmt.Errorf("read status must be unread, reading or finished")
	}
	var finishedAt time.Time
	if status == "finished" {
		finishedAt = time.Now()
	}
	return s.store.SetDocumentReadStatus(ctx, document.ID, status, finishedAt)
}

func (s *Service) Search(ctx context.Context, query string, limit int, exact bool) ([]port.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		return nil, errors.New("search limit cannot exceed 1000")
	}
	return s.store.Search(ctx, query, limit, exact)
}

// Grep finds documents containing a literal substring (rg -i semantics).
func (s *Service) Grep(ctx context.Context, pattern string, limit int) ([]port.SearchHit, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("grep pattern is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return nil, errors.New("grep limit cannot exceed 100")
	}
	return s.store.GrepDocuments(ctx, pattern, limit)
}

// SuggestDocuments finds active documents by UUID, title, or path substring.
// Unlike full-text Search, it is intended for compact picker/autocomplete UIs.
func (s *Service) SuggestDocuments(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("suggestion query is required")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		return nil, errors.New("suggestion limit cannot exceed 100")
	}
	return s.store.SuggestDocuments(ctx, query, limit)
}

func (s *Service) ResolveDocument(ctx context.Context, selector string) (*catalog.Document, string, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, "", errors.New("document selector is required")
	}
	return s.store.ResolveDocument(ctx, selector)
}

func (s *Service) ReadDocument(ctx context.Context, selector string) ([]byte, error) {
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return nil, err
	}
	if document.Status != catalog.DocumentActive {
		return nil, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	return s.reader.Read(ctx, absolute)
}

// ReadDocumentText returns user-readable text. Markdown is read directly;
// PDFs are re-extracted from their filesystem authority instead of exposing
// binary bytes to agents or summarizers.
func (s *Service) ReadDocumentText(ctx context.Context, selector string) ([]byte, error) {
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return nil, err
	}
	if document.Status != catalog.DocumentActive {
		return nil, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	if document.Index.MediaType == "text/markdown" {
		return s.reader.Read(ctx, absolute)
	}
	if document.Index.MediaType != "application/pdf" {
		return nil, fmt.Errorf("text extraction does not support %s", document.Index.MediaType)
	}
	observation, err := s.scanner.ObserveFile(ctx, document.Location, absolute)
	if err != nil {
		return nil, err
	}
	return observation.SearchText, nil
}

type ToggleDocumentPinResult struct {
	DocumentID catalog.DocumentID
	Pinned     bool
}

// SetDocumentSummary stores a summary for the document identified by
// selector. The summary lives in index metadata and survives rescans.
func (s *Service) SetDocumentSummary(ctx context.Context, selector, summary string) (catalog.DocumentID, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return "", lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return "", err
	}
	if err := s.store.SetDocumentSummary(ctx, document.ID, summary); err != nil {
		return "", err
	}
	return document.ID, nil
}

func (s *Service) ToggleDocumentPin(ctx context.Context, selector string) (ToggleDocumentPinResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return ToggleDocumentPinResult{}, lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return ToggleDocumentPinResult{}, err
	}
	document.SetPinned(!document.Pinned)
	if err := s.store.SavePinned(ctx, document.ID, document.Pinned); err != nil {
		return ToggleDocumentPinResult{}, err
	}
	return ToggleDocumentPinResult{DocumentID: document.ID, Pinned: document.Pinned}, nil
}

// SaveAnnotations is the legacy-sidecar migration adapter. New note content
// must use SaveAnnotationNote; an empty value clears the compatibility blob.
func (s *Service) SaveAnnotations(ctx context.Context, selector string, sidecar string) (catalog.DocumentID, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return "", lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return "", err
	}
	if err := s.store.SaveAnnotations(ctx, document.ID, sidecar); err != nil {
		return "", err
	}
	return document.ID, nil
}

// GetAnnotations returns the legacy stored annotation sidecar. It is retained
// only so existing databases can be migrated to Markdown annotation notes.
func (s *Service) GetAnnotations(ctx context.Context, selector string) (string, error) {
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return "", err
	}
	return s.store.GetAnnotations(ctx, document.ID)
}

type SaveAnnotationNoteOptions struct {
	TargetSelector string
	NoteSelector   string // empty creates a new *-note.md document
	Title          string
	Body           string // empty leaves an existing note document unchanged
	Start          int
	Prefix         string
	Suffix         string
	Highlight      bool
	Underline      bool
	Strikethrough  bool
	// Kind is optional note subtype: "" (plain) or "qa" (assist Q&A).
	Kind string
}

// Annotation note kinds stored on annotation_notes.kind and note front matter.
const (
	AnnotationNoteKindPlain   = ""
	AnnotationNoteKindQA      = "qa"
	AnnotationNoteKindSummary = "summary"
)

// NormalizeAnnotationNoteKind returns a canonical kind or empty for plain notes.
func NormalizeAnnotationNoteKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case AnnotationNoteKindQA:
		return AnnotationNoteKindQA
	case AnnotationNoteKindSummary:
		return AnnotationNoteKindSummary
	default:
		return AnnotationNoteKindPlain
	}
}

// DetectAnnotationNoteKind infers kind from note Markdown (front matter or Q: body).
func DetectAnnotationNoteKind(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return AnnotationNoteKindPlain
	}
	if strings.HasPrefix(body, "---") {
		rest := body[3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			fm := rest[:end]
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "kind:") {
					val := strings.TrimSpace(strings.TrimPrefix(line, "kind:"))
					val = strings.Trim(val, `"'`)
					return NormalizeAnnotationNoteKind(val)
				}
			}
		}
	}
	if strings.Contains(body, "**Q:**") || strings.Contains(body, "**Q**:") {
		return AnnotationNoteKindQA
	}
	return AnnotationNoteKindPlain
}

type SaveAnnotationNoteResult struct {
	Record port.AnnotationNoteRecord
	Note   *catalog.Document
	Path   string
}

// SaveAnnotationNote creates or updates the Markdown document that contains a
// selection note, then stores only its target/anchor metadata in SQLite.
func (s *Service) SaveAnnotationNote(ctx context.Context, opts SaveAnnotationNoteOptions) (SaveAnnotationNoteResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return SaveAnnotationNoteResult{}, lockErr
	}
	defer release()
	target, _, err := s.ResolveDocument(ctx, opts.TargetSelector)
	if err != nil {
		return SaveAnnotationNoteResult{}, err
	}
	if opts.Start < 0 {
		return SaveAnnotationNoteResult{}, errors.New("annotation start must not be negative")
	}

	var note *catalog.Document
	var absolute string
	contentChanged := false
	if strings.TrimSpace(opts.NoteSelector) == "" {
		if strings.TrimSpace(opts.Body) == "" {
			return SaveAnnotationNoteResult{}, errors.New("annotation note Markdown is required")
		}
		title := strings.TrimSpace(opts.Title)
		if title == "" {
			title = "Selection note"
		}
		created, createErr := s.CreateNote(ctx, CreateNoteOptions{Title: title, Body: opts.Body, ClipMode: "selection"})
		if createErr != nil {
			return SaveAnnotationNoteResult{}, createErr
		}
		note, absolute = created.Document, created.Path
		contentChanged = true
	} else {
		note, absolute, err = s.ResolveDocument(ctx, opts.NoteSelector)
		if err != nil {
			return SaveAnnotationNoteResult{}, err
		}
		if strings.TrimSpace(opts.Body) != "" {
			current, readErr := s.reader.Read(ctx, absolute)
			if readErr != nil {
				return SaveAnnotationNoteResult{}, readErr
			}
			if string(current) != opts.Body {
				if _, syncErr := s.SyncDocument(ctx, string(note.ID), opts.Body); syncErr != nil {
					return SaveAnnotationNoteResult{}, syncErr
				}
				note, absolute, err = s.ResolveDocument(ctx, string(note.ID))
				if err != nil {
					return SaveAnnotationNoteResult{}, err
				}
				contentChanged = true
			}
		}
	}
	if note.ID == target.ID {
		return SaveAnnotationNoteResult{}, errors.New("a document cannot annotate itself")
	}
	now := s.clock.Now()
	// Keep annotation notes visible through the existing UUID link/backlink
	// graph. This edge is an identity relation, not a content projection.
	edge, edgeErr := catalog.NewGraphEdge(target.ID, note.ID, catalog.EdgeManual, now)
	if edgeErr != nil {
		return SaveAnnotationNoteResult{}, edgeErr
	}
	if _, edgeErr = s.store.AddEdge(ctx, edge); edgeErr != nil {
		return SaveAnnotationNoteResult{}, edgeErr
	}
	kind := NormalizeAnnotationNoteKind(opts.Kind)
	if kind == "" && strings.TrimSpace(opts.Body) != "" {
		kind = DetectAnnotationNoteKind(opts.Body)
	}
	record := port.AnnotationNoteRecord{
		NoteDocumentID: note.ID, TargetDocumentID: target.ID, Start: opts.Start,
		Prefix: opts.Prefix, Suffix: opts.Suffix, Highlight: opts.Highlight,
		Underline: opts.Underline, Strikethrough: opts.Strikethrough, Kind: kind,
		CreatedAt: now, UpdatedAt: now,
	}
	if existing, ok, getErr := s.store.GetAnnotationNote(ctx, note.ID); getErr != nil {
		return SaveAnnotationNoteResult{}, getErr
	} else if ok {
		record.CreatedAt = existing.CreatedAt
		if kind == "" {
			record.Kind = existing.Kind
		}
		if !contentChanged && existing.TargetDocumentID == record.TargetDocumentID &&
			existing.Start == record.Start && existing.Prefix == record.Prefix && existing.Suffix == record.Suffix &&
			existing.Highlight == record.Highlight && existing.Underline == record.Underline && existing.Strikethrough == record.Strikethrough &&
			existing.Kind == record.Kind {
			return SaveAnnotationNoteResult{Record: existing, Note: note, Path: absolute}, nil
		}
	}
	if err := s.store.UpsertAnnotationNote(ctx, record); err != nil {
		return SaveAnnotationNoteResult{}, err
	}
	return SaveAnnotationNoteResult{Record: record, Note: note, Path: absolute}, nil
}

func (s *Service) ListAnnotationNotes(ctx context.Context, targetSelector string) (*catalog.Document, []port.AnnotationNoteRecord, error) {
	target, _, err := s.ResolveDocument(ctx, targetSelector)
	if err != nil {
		return nil, nil, err
	}
	records, err := s.store.ListAnnotationNotes(ctx, target.ID)
	return target, records, err
}

func (s *Service) GetAnnotationNote(ctx context.Context, noteSelector string) (port.AnnotationNoteRecord, bool, error) {
	note, _, err := s.ResolveDocument(ctx, noteSelector)
	if err != nil {
		return port.AnnotationNoteRecord{}, false, err
	}
	return s.store.GetAnnotationNote(ctx, note.ID)
}

func (s *Service) DeleteAnnotationNote(ctx context.Context, targetSelector, noteSelector string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	target, _, err := s.ResolveDocument(ctx, targetSelector)
	if err != nil {
		return err
	}
	note, _, err := s.ResolveDocument(ctx, noteSelector)
	if err != nil {
		return err
	}
	record, ok, err := s.store.GetAnnotationNote(ctx, note.ID)
	if err != nil {
		return err
	}
	if !ok || record.TargetDocumentID != target.ID {
		return fmt.Errorf("document %s is not an annotation note for %s", note.ID, target.ID)
	}
	if _, _, err := s.TrashDocumentFile(ctx, string(note.ID)); err != nil {
		return err
	}
	// Soft delete: the UUID relation and graph edge survive (hidden by the
	// trash filter), so restoring the note revives the annotation unchanged.
	return nil
}

func (s *Service) SaveDocumentReadState(ctx context.Context, selector string, progressY int, progressAt string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return err
	}
	if progressY < 0 {
		progressY = 0
	}
	return s.store.SaveDocumentReadState(ctx, port.DocumentReadState{DocumentID: document.ID, ProgressY: progressY, ProgressAt: progressAt})
}

func (s *Service) GetDocumentReadState(ctx context.Context, selector string) (port.DocumentReadState, bool, error) {
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return port.DocumentReadState{}, false, err
	}
	return s.store.GetDocumentReadState(ctx, document.ID)
}

// MarkDocumentOpened updates recency without disturbing the reader's saved
// scroll position.
func (s *Service) MarkDocumentOpened(ctx context.Context, selector string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return err
	}
	state, found, err := s.store.GetDocumentReadState(ctx, document.ID)
	if err != nil {
		return err
	}
	if !found {
		state = port.DocumentReadState{DocumentID: document.ID}
	}
	state.ProgressAt = s.clock.Now().UTC().Format(time.RFC3339Nano)
	return s.store.SaveDocumentReadState(ctx, state)
}

func (s *Service) ListRecentDocuments(ctx context.Context, limit int) ([]port.RecentDocument, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		return nil, errors.New("recent document limit cannot exceed 100")
	}
	return s.store.ListRecentDocuments(ctx, limit)
}

func (s *Service) ListRecentlyModifiedDocuments(ctx context.Context, limit int) ([]port.ModifiedDocument, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		return nil, errors.New("modified document limit cannot exceed 100")
	}
	return s.store.ListRecentlyModifiedDocuments(ctx, limit)
}

type SyncDocumentResult struct {
	DocumentID catalog.DocumentID
	Path       string
}

// SyncDocument writes Markdown back to an existing active source file and
// refreshes its catalog/FTS observation. Annotation notes are separate Markdown
// documents and are reconciled by the annotation application flow.
func (s *Service) SyncDocument(ctx context.Context, selector, body string) (SyncDocumentResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return SyncDocumentResult{}, lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return SyncDocumentResult{}, err
	}
	if document.Status != catalog.DocumentActive {
		return SyncDocumentResult{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	if document.Index.MediaType != "text/markdown" {
		return SyncDocumentResult{}, fmt.Errorf("sync text does not support %s documents", document.Index.MediaType)
	}
	if strings.TrimSpace(body) == "" {
		return SyncDocumentResult{}, errors.New("Markdown body is required")
	}
	if err := s.writer.Write(ctx, absolute, []byte(body)); err != nil {
		return SyncDocumentResult{}, err
	}
	observation, err := s.scanner.ObserveFile(ctx, document.Location, absolute)
	if err != nil {
		return SyncDocumentResult{}, err
	}
	if err := document.Observe(observation, s.clock.Now()); err != nil {
		return SyncDocumentResult{}, err
	}
	if err := s.saveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true}); err != nil {
		return SyncDocumentResult{}, err
	}
	return SyncDocumentResult{DocumentID: document.ID, Path: absolute}, nil
}

func (s *Service) ReindexDocument(ctx context.Context, selector string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return err
	}
	if document.Status != catalog.DocumentActive {
		return fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	observation, err := s.scanner.ObserveFile(ctx, document.Location, absolute)
	if err != nil {
		return err
	}
	if err := document.Observe(observation, s.clock.Now()); err != nil {
		return err
	}
	return s.saveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true})
}

func (s *Service) DeleteDocumentFile(ctx context.Context, selector string) (catalog.Document, string, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return catalog.Document{}, "", lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return catalog.Document{}, "", err
	}
	if document.Status != catalog.DocumentActive {
		return catalog.Document{}, "", fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	if err := s.writer.Remove(ctx, absolute); err != nil {
		return catalog.Document{}, "", err
	}
	indexedPath, err := s.store.ResolvePath(ctx, strconv.FormatInt(int64(document.Location.PathID), 10), false)
	if err != nil {
		return *document, absolute, err
	}
	if _, err := s.scanOne(ctx, indexedPath); err != nil {
		return *document, absolute, err
	}
	return *document, absolute, nil
}

// TrashDocumentFile soft-deletes a document: its source file moves into the
// path root's hidden trash directory and a trash record hides the document
// from listings, search, graphs, and annotation DTOs. UUID, relations, index,
// and FTS entries stay intact so RestoreDocument is a plain move back.
func (s *Service) TrashDocumentFile(ctx context.Context, selector string) (catalog.Document, string, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return catalog.Document{}, "", lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return catalog.Document{}, "", err
	}
	if document.Status != catalog.DocumentActive {
		return catalog.Document{}, "", fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	if _, trashed, trashErr := s.store.GetTrashedDocument(ctx, document.ID); trashErr != nil {
		return catalog.Document{}, "", trashErr
	} else if trashed {
		return catalog.Document{}, "", fmt.Errorf("document %s is already in the trash", document.ID)
	}
	indexedPath, err := s.store.ResolvePath(ctx, strconv.FormatInt(int64(document.Location.PathID), 10), false)
	if err != nil {
		return catalog.Document{}, "", err
	}
	trashDir := filepath.Join(indexedPath.Root, catalog.TrashDir)
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return catalog.Document{}, "", fmt.Errorf("creating trash directory: %w", err)
	}
	dest := filepath.Base(absolute)
	ext := filepath.Ext(dest)
	stem := strings.TrimSuffix(dest, ext)
	for i := 2; fileExists(filepath.Join(trashDir, dest)); i++ {
		dest = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	trashAbs := filepath.Join(trashDir, dest)
	if err := s.writer.Move(ctx, absolute, trashAbs); err != nil {
		return catalog.Document{}, "", err
	}
	origin := document.Location.RelativePath
	if err := s.store.SetDocumentRelativePath(ctx, document.ID, catalog.TrashDir+"/"+filepath.ToSlash(dest)); err != nil {
		return catalog.Document{}, "", err
	}
	if err := s.store.TrashDocument(ctx, document.ID, origin, s.clock.Now()); err != nil {
		return catalog.Document{}, "", err
	}
	return *document, trashAbs, nil
}

// RestoreDocument moves a trashed document back to its original location and
// unhides it. Fails when the origin path is occupied again.
func (s *Service) RestoreDocument(ctx context.Context, selector string) (catalog.Document, string, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return catalog.Document{}, "", lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return catalog.Document{}, "", err
	}
	trash, ok, err := s.store.GetTrashedDocument(ctx, document.ID)
	if err != nil {
		return catalog.Document{}, "", err
	}
	if !ok {
		return catalog.Document{}, "", fmt.Errorf("document %s is not in the trash", document.ID)
	}
	indexedPath, err := s.store.ResolvePath(ctx, strconv.FormatInt(int64(document.Location.PathID), 10), false)
	if err != nil {
		return catalog.Document{}, "", err
	}
	originAbs := filepath.Join(indexedPath.Root, filepath.FromSlash(trash.OriginRelativePath))
	if fileExists(originAbs) {
		return catalog.Document{}, "", fmt.Errorf("cannot restore %s: %s already exists", document.ID, trash.OriginRelativePath)
	}
	if err := os.MkdirAll(filepath.Dir(originAbs), 0o755); err != nil {
		return catalog.Document{}, "", fmt.Errorf("preparing restore directory: %w", err)
	}
	if err := s.writer.Move(ctx, absolute, originAbs); err != nil {
		return catalog.Document{}, "", err
	}
	if err := s.store.SetDocumentRelativePath(ctx, document.ID, trash.OriginRelativePath); err != nil {
		return catalog.Document{}, "", err
	}
	if err := s.store.DeleteTrashRecord(ctx, document.ID); err != nil {
		return catalog.Document{}, "", err
	}
	return *document, originAbs, nil
}

// PurgeTrashedDocuments physically deletes trashed documents. A zero
// olderThan purges everything; otherwise only items trashed strictly before
// it. Returns how many documents were removed and how many bytes were freed.
func (s *Service) PurgeTrashedDocuments(ctx context.Context, olderThan time.Time) (int, int64, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return 0, 0, lockErr
	}
	defer release()
	records, trashRecords, err := s.store.ListTrashedDocuments(ctx)
	if err != nil {
		return 0, 0, err
	}
	byID := make(map[catalog.DocumentID]port.TrashRecord, len(trashRecords))
	for _, record := range trashRecords {
		byID[record.DocumentID] = record
	}
	var removed int
	var freed int64
	roots := map[catalog.IndexedPathID]string{}
	for _, record := range records {
		trash, ok := byID[record.Document.ID]
		if !ok {
			continue
		}
		if !olderThan.IsZero() && !trash.TrashedAt.Before(olderThan) {
			continue
		}
		if fileExists(record.AbsolutePath) {
			if err := s.writer.Remove(ctx, record.AbsolutePath); err != nil {
				return removed, freed, err
			}
		}
		if err := s.store.PurgeDocument(ctx, record.Document.ID); err != nil {
			return removed, freed, err
		}
		if indexedPath, pathErr := s.store.ResolvePath(ctx, strconv.FormatInt(int64(record.Document.Location.PathID), 10), false); pathErr == nil {
			roots[indexedPath.ID] = indexedPath.Root
		}
		removed++
		freed += record.Document.Index.Size
	}
	for _, root := range roots {
		// Best effort: the directory only disappears when it is empty.
		_ = os.Remove(filepath.Join(root, catalog.TrashDir))
	}
	return removed, freed, nil
}

// TrashSummary reports how many documents are trashed and their total size.
func (s *Service) TrashSummary(ctx context.Context) (int, int64, error) {
	records, _, err := s.store.ListTrashedDocuments(ctx)
	if err != nil {
		return 0, 0, err
	}
	var size int64
	for _, record := range records {
		size += record.Document.Index.Size
	}
	return len(records), size, nil
}

// ListTrashedDocuments returns trashed documents with their trash records.
func (s *Service) ListTrashedDocuments(ctx context.Context) ([]port.DocumentRecord, []port.TrashRecord, error) {
	return s.store.ListTrashedDocuments(ctx)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type RenameDocumentResult struct {
	DocumentID catalog.DocumentID
	Path       string
	Title      string
}

// RenameDocument moves an active source file to a new filename in the same
// directory and re-points the existing Document at the new location. The
// stable UUID, graph links, source URLs, and annotation relations survive
// untouched because only the location changes.
//
// For Markdown, displayTitle is written into YAML front matter and the first
// ATX H1. For PDF, it only updates searchable catalog metadata; binary bytes
// are never rewritten.
func (s *Service) RenameDocument(ctx context.Context, selector, newFilename, displayTitle string) (RenameDocumentResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return RenameDocumentResult{}, lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	if document.Status != catalog.DocumentActive {
		return RenameDocumentResult{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	newFilename = strings.TrimSpace(newFilename)
	if newFilename == "" || newFilename == "." || newFilename == ".." ||
		strings.ContainsAny(newFilename, "/\\") || strings.HasPrefix(newFilename, ".") {
		return RenameDocumentResult{}, fmt.Errorf("invalid filename %q", newFilename)
	}
	ext := strings.ToLower(filepath.Ext(newFilename))
	isPDF := document.Index.MediaType == "application/pdf"
	if isPDF {
		if ext != ".pdf" {
			return RenameDocumentResult{}, errors.New("renamed PDF files must keep the .pdf extension")
		}
	} else if ext != ".md" && ext != ".markdown" {
		return RenameDocumentResult{}, errors.New("renamed Markdown files must keep .md or .markdown")
	}
	requestedTitle := strings.TrimSpace(displayTitle) != ""
	displayTitle = strings.TrimSpace(displayTitle)
	if displayTitle == "" {
		if isPDF {
			displayTitle = document.Index.Title
		} else {
			displayTitle = strings.TrimSuffix(newFilename, filepath.Ext(newFilename))
		}
	}

	target := absolute
	moved := false
	if filepath.Base(absolute) != newFilename {
		target = filepath.Join(filepath.Dir(absolute), newFilename)
		if _, err := os.Stat(target); err == nil {
			return RenameDocumentResult{}, fmt.Errorf("%s already exists", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return RenameDocumentResult{}, err
		}
		if err := s.writer.Move(ctx, absolute, target); err != nil {
			return RenameDocumentResult{}, err
		}
		moved = true
	}

	body, err := s.reader.Read(ctx, target)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	if !isPDF {
		rewritten, changed := rewriteMarkdownDisplayTitle(body, displayTitle)
		if changed {
			if err := s.writer.Write(ctx, target, rewritten); err != nil {
				return RenameDocumentResult{}, err
			}
			body = rewritten
		}
	}

	relative := document.Location.RelativePath
	if moved {
		relative = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(document.Location.RelativePath)), newFilename))
	}
	location, err := catalog.NewLocation(document.Location.PathID, relative)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	observation, err := s.scanner.ObserveFile(ctx, location, target)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	// ObserveFile re-reads disk; ensure an explicit PDF title or rewritten
	// Markdown title remains authoritative in the catalog.
	if (!isPDF && observation.Title != displayTitle) || (isPDF && requestedTitle) {
		observation.Title = displayTitle
		observation.Body = body
	}
	if moved {
		if err := document.Relocate(location, observation, s.clock.Now()); err != nil {
			return RenameDocumentResult{}, err
		}
	} else {
		if err := document.Observe(observation, s.clock.Now()); err != nil {
			return RenameDocumentResult{}, err
		}
	}
	if isPDF && requestedTitle {
		document.Index.Title = displayTitle
		document.Index.MetadataOverrides |= catalog.MetadataTitleOverride
	}
	if err := s.saveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true}); err != nil {
		return RenameDocumentResult{}, err
	}
	return RenameDocumentResult{DocumentID: document.ID, Path: target, Title: displayTitle}, nil
}

// rewriteMarkdownDisplayTitle updates YAML front matter title: and the first
// ATX H1 so display name, filename, and body stay consistent after a rename.
func rewriteMarkdownDisplayTitle(body []byte, title string) ([]byte, bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return body, false
	}
	text := string(body)
	lines := strings.Split(text, "\n")
	changed := false
	quoted := strconv.Quote(title)

	// Front matter title:
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			trim := strings.TrimSpace(lines[i])
			if trim == "---" || trim == "..." {
				break
			}
			raw := lines[i]
			indentLen := len(raw) - len(strings.TrimLeft(raw, " \t"))
			indent, rest := raw[:indentLen], raw[indentLen:]
			key, _, ok := strings.Cut(rest, ":")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "title") {
				continue
			}
			next := indent + "title: " + quoted
			if lines[i] != next {
				lines[i] = next
				changed = true
			}
			break
		}
	}

	// First ATX H1 outside fences/front matter.
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if i == 0 && trim == "---" {
			continue
		}
		if inFrontmatter {
			if trim == "---" || trim == "..." {
				inFrontmatter = false
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			mark := trim[:3]
			if !inFence {
				inFence, fenceMarker = true, mark
			} else if strings.HasPrefix(trim, fenceMarker) {
				inFence, fenceMarker = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trim, "# ") && !strings.HasPrefix(trim, "##") {
			next := "# " + title
			if line != next {
				// Keep leading indentation if any.
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = indent + next
				changed = true
			}
			break
		}
	}
	if !changed {
		return body, false
	}
	return []byte(strings.Join(lines, "\n")), true
}
