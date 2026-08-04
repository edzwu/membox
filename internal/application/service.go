package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

type Service struct {
	store   port.CatalogStore
	scanner port.MarkdownScanner
	reader  port.ContentReader
	writer  port.ContentWriter
	ids     port.IDGenerator
	clock   port.Clock
	history port.GitHistory
}

func NewService(store port.CatalogStore, scanner port.MarkdownScanner, reader port.ContentReader, writer port.ContentWriter, ids port.IDGenerator, clock port.Clock, history port.GitHistory) *Service {
	return &Service{store: store, scanner: scanner, reader: reader, writer: writer, ids: ids, clock: clock, history: history}
}

func (s *Service) Close() error { return s.store.Close() }

type AddPathResult struct {
	Path          port.PathSummary
	AlreadyExists bool
	Scan          ScanReport
}

type ScanOptions struct {
	Selector        string
	TimestampSource string
}

type ScanReport struct {
	Paths               int
	Files               int
	Added               int
	Updated             int
	Renamed             int
	Unchanged           int
	Missing             int
	PossibleRenames     int
	Errors              int
	TimestampSource     string
	GitPaths            int
	TimestampsUpdated   int
	TimestampsUnchanged int
	NoGitHistory        int
	NonGitPaths         int
}

func (r *ScanReport) Add(other ScanReport) {
	r.Paths += other.Paths
	r.Files += other.Files
	r.Added += other.Added
	r.Updated += other.Updated
	r.Renamed += other.Renamed
	r.Unchanged += other.Unchanged
	r.Missing += other.Missing
	r.PossibleRenames += other.PossibleRenames
	r.Errors += other.Errors
}

func (s *Service) AddPath(ctx context.Context, directory string) (AddPathResult, error) {
	canonical, err := s.scanner.Canonicalize(directory)
	if err != nil {
		return AddPathResult{}, err
	}
	indexedPath, existing, err := s.store.AddOrReactivatePath(ctx, canonical, s.clock.Now())
	if err != nil {
		return AddPathResult{}, err
	}
	if existing && indexedPath.Status != catalog.PathRemoved {
		list, listErr := s.store.ListPaths(ctx, true)
		if listErr != nil {
			return AddPathResult{}, listErr
		}
		return AddPathResult{Path: findPathSummary(list, indexedPath.ID), AlreadyExists: true}, nil
	}
	report, scanErr := s.scanOne(ctx, indexedPath)
	list, listErr := s.store.ListPaths(ctx, true)
	if listErr != nil {
		return AddPathResult{}, errors.Join(scanErr, listErr)
	}
	return AddPathResult{Path: findPathSummary(list, indexedPath.ID), Scan: report}, scanErr
}

func findPathSummary(paths []port.PathSummary, id catalog.IndexedPathID) port.PathSummary {
	for _, p := range paths {
		if p.Path.ID == id {
			return p
		}
	}
	return port.PathSummary{}
}

func (s *Service) ListPaths(ctx context.Context) ([]port.PathSummary, error) {
	return s.store.ListPaths(ctx, false)
}

type RemovePathResult struct {
	Path      catalog.IndexedPath
	Documents int
}

func (s *Service) RemovePath(ctx context.Context, selector string) (RemovePathResult, error) {
	indexedPath, err := s.resolvePath(ctx, selector)
	if err != nil {
		return RemovePathResult{}, err
	}
	count, err := s.store.RemovePath(ctx, indexedPath.ID, s.clock.Now())
	if err != nil {
		return RemovePathResult{}, err
	}
	indexedPath.Remove()
	return RemovePathResult{Path: *indexedPath, Documents: count}, nil
}

func (s *Service) ScanPaths(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	timestampSource := strings.ToLower(strings.TrimSpace(opts.TimestampSource))
	if timestampSource == "" {
		timestampSource = "filesystem"
	}
	if timestampSource != "filesystem" && timestampSource != "git" {
		return ScanReport{}, fmt.Errorf("unsupported timestamp source %q; use filesystem or git", opts.TimestampSource)
	}

	var paths []*catalog.IndexedPath
	if strings.TrimSpace(opts.Selector) != "" {
		p, err := s.resolvePath(ctx, opts.Selector)
		if err != nil {
			return ScanReport{}, err
		}
		paths = []*catalog.IndexedPath{p}
	} else {
		summaries, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return ScanReport{}, err
		}
		for i := range summaries {
			p := summaries[i].Path
			paths = append(paths, &p)
		}
	}

	report := ScanReport{TimestampSource: timestampSource}
	var scanErrors []error
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		one, err := s.scanOne(ctx, p)
		report.Add(one)
		if err != nil {
			scanErrors = append(scanErrors, err)
		}
	}
	if timestampSource == "git" {
		gitReport, err := s.syncGitTimes(ctx, syncGitTimesOptions{Selector: opts.Selector})
		report.GitPaths = gitReport.GitPaths
		report.TimestampsUpdated = gitReport.Updated
		report.TimestampsUnchanged = gitReport.Unchanged
		report.NoGitHistory = gitReport.NoHistory
		report.NonGitPaths = gitReport.NonGitPaths
		report.Errors += gitReport.Errors
		if err != nil {
			scanErrors = append(scanErrors, err)
		}
	}
	return report, errors.Join(scanErrors...)
}

func (s *Service) scanOne(ctx context.Context, indexedPath *catalog.IndexedPath) (ScanReport, error) {
	result, scanErr := s.scanner.Scan(ctx, *indexedPath)
	report := ScanReport{Paths: 1, Files: len(result.Observations), Errors: len(result.Issues)}
	if scanErr != nil {
		report.Errors++
		indexedPath.RecordScan(s.clock.Now(), scanErr)
		if saveErr := s.store.SaveScan(ctx, indexedPath, nil); saveErr != nil {
			return report, errors.Join(scanErr, saveErr)
		}
		return report, scanErr
	}

	existing, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return report, err
	}
	byRelative := make(map[string]*catalog.Document, len(existing))
	for _, d := range existing {
		byRelative[d.Location.RelativePath] = d
	}

	now := s.clock.Now()
	var saves []port.ScanSave
	var newObservations []catalog.Observation
	seen := make(map[string]bool)
	for _, observation := range result.Observations {
		rel := observation.Location.RelativePath
		seen[rel] = true
		if document, ok := byRelative[rel]; ok {
			unchanged := document.Status == catalog.DocumentActive &&
				document.Index.SHA256 == observation.SHA256 && document.Index.Size == observation.Size &&
				document.Index.MTime == observation.MTime && document.FileKey == observation.FileKey
			if err := document.Observe(observation, now); err != nil {
				return report, err
			}
			saves = append(saves, port.ScanSave{Document: document, Body: observation.Body, Reindex: true})
			if unchanged {
				report.Unchanged++
			} else {
				report.Updated++
			}
			continue
		}
		newObservations = append(newObservations, observation)
	}

	var justMissing []*catalog.Document
	var alreadyMissing []*catalog.Document
	for _, document := range existing {
		if seen[document.Location.RelativePath] {
			continue
		}
		if catalog.IsTrashedPath(document.Location.RelativePath) {
			// Trashed documents live in the skipped trash directory; scans must
			// never mark them missing or reconcile them away.
			continue
		}
		if document.Status == catalog.DocumentActive {
			justMissing = append(justMissing, document)
		} else {
			alreadyMissing = append(alreadyMissing, document)
		}
	}

	reconciled := catalog.ReconcileRenames(justMissing, newObservations)
	for _, relocation := range reconciled.Relocations {
		if err := relocation.Document.Relocate(relocation.Observation.Location, relocation.Observation, now); err != nil {
			return report, err
		}
		saves = append(saves, port.ScanSave{Document: relocation.Document, Body: relocation.Observation.Body, Reindex: true})
		report.Renamed++
	}
	for _, document := range reconciled.Missing {
		document.MarkMissing(now)
		saves = append(saves, port.ScanSave{Document: document})
		report.Missing++
	}
	_ = alreadyMissing // retained without rewriting their last-known state

	for _, observation := range reconciled.New {
		id, idErr := s.ids.NewDocumentID()
		if idErr != nil {
			return report, idErr
		}
		document, createErr := catalog.NewDocument(id, observation, now)
		if createErr != nil {
			return report, createErr
		}
		saves = append(saves, port.ScanSave{Document: document, Body: observation.Body, Reindex: true})
		report.Added++
	}
	report.PossibleRenames = len(reconciled.Candidates)

	var issueErr error
	if len(result.Issues) > 0 {
		parts := make([]error, 0, len(result.Issues))
		for _, issue := range result.Issues {
			parts = append(parts, fmt.Errorf("%s: %w", issue.Path, issue.Err))
		}
		issueErr = errors.Join(parts...)
	}
	indexedPath.RecordScan(now, issueErr)
	if err := s.store.SaveScan(ctx, indexedPath, saves); err != nil {
		return report, err
	}
	if issueErr != nil {
		return report, issueErr
	}
	return report, nil
}

type syncGitTimesOptions struct{ Selector string }

type syncGitTimesReport struct {
	Paths       int
	GitPaths    int
	Documents   int
	Updated     int
	Unchanged   int
	NoHistory   int
	NonGitPaths int
	Errors      int
}

// syncGitTimes replaces filesystem-derived source dates with the first and
// latest author dates in Git. Files without history and non-Git paths are
// intentionally left unchanged.
func (s *Service) syncGitTimes(ctx context.Context, opts syncGitTimesOptions) (syncGitTimesReport, error) {
	var paths []*catalog.IndexedPath
	if strings.TrimSpace(opts.Selector) != "" {
		path, err := s.resolvePath(ctx, opts.Selector)
		if err != nil {
			return syncGitTimesReport{}, err
		}
		paths = []*catalog.IndexedPath{path}
	} else {
		summaries, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return syncGitTimesReport{}, err
		}
		for i := range summaries {
			path := summaries[i].Path
			paths = append(paths, &path)
		}
	}

	report := syncGitTimesReport{Paths: len(paths)}
	var syncErrors []error
	for _, indexedPath := range paths {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		repositoryRoot, applicable, err := s.history.RepositoryRoot(ctx, indexedPath.Root)
		if err != nil {
			report.Errors++
			syncErrors = append(syncErrors, err)
			continue
		}
		if !applicable {
			report.NonGitPaths++
			continue
		}
		report.GitPaths++
		documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
		if err != nil {
			report.Errors++
			syncErrors = append(syncErrors, err)
			continue
		}
		report.Documents += len(documents)
		var changed []*catalog.Document
		for _, document := range documents {
			absolutePath := filepath.Join(indexedPath.Root, filepath.FromSlash(document.Location.RelativePath))
			times, found, err := s.history.FileTimes(ctx, repositoryRoot, absolutePath)
			if err != nil {
				report.Errors++
				syncErrors = append(syncErrors, err)
				continue
			}
			if !found {
				report.NoHistory++
				continue
			}
			if document.Index.SourceCreatedAt.Equal(times.CreatedAt) && document.Index.SourceUpdatedAt.Equal(times.UpdatedAt) {
				report.Unchanged++
				continue
			}
			if err := document.SetSourceTimes(times.CreatedAt, times.UpdatedAt); err != nil {
				report.Errors++
				syncErrors = append(syncErrors, fmt.Errorf("updating source times for %q: %w", absolutePath, err))
				continue
			}
			changed = append(changed, document)
		}
		if len(changed) > 0 {
			if err := s.store.SaveSourceTimes(ctx, changed); err != nil {
				report.Errors++
				syncErrors = append(syncErrors, err)
				continue
			}
			report.Updated += len(changed)
		}
	}
	return report, errors.Join(syncErrors...)
}

func (s *Service) ListDocuments(ctx context.Context, limit int, includeUnavailable bool) ([]port.DocumentRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return nil, errors.New("document list limit cannot exceed 1000")
	}
	return s.store.ListDocuments(ctx, limit, includeUnavailable)
}

func (s *Service) Search(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
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
	return s.store.Search(ctx, query, limit)
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

type ToggleDocumentPinResult struct {
	DocumentID catalog.DocumentID
	Pinned     bool
}

func (s *Service) ToggleDocumentPin(ctx context.Context, selector string) (ToggleDocumentPinResult, error) {
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
}

type SaveAnnotationNoteResult struct {
	Record port.AnnotationNoteRecord
	Note   *catalog.Document
	Path   string
}

// SaveAnnotationNote creates or updates the Markdown document that contains a
// selection note, then stores only its target/anchor metadata in SQLite.
func (s *Service) SaveAnnotationNote(ctx context.Context, opts SaveAnnotationNoteOptions) (SaveAnnotationNoteResult, error) {
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
	record := port.AnnotationNoteRecord{
		NoteDocumentID: note.ID, TargetDocumentID: target.ID, Start: opts.Start,
		Prefix: opts.Prefix, Suffix: opts.Suffix, Highlight: opts.Highlight,
		Underline: opts.Underline, Strikethrough: opts.Strikethrough,
		CreatedAt: now, UpdatedAt: now,
	}
	if existing, ok, getErr := s.store.GetAnnotationNote(ctx, note.ID); getErr != nil {
		return SaveAnnotationNoteResult{}, getErr
	} else if ok {
		record.CreatedAt = existing.CreatedAt
		if !contentChanged && existing.TargetDocumentID == record.TargetDocumentID &&
			existing.Start == record.Start && existing.Prefix == record.Prefix && existing.Suffix == record.Suffix &&
			existing.Highlight == record.Highlight && existing.Underline == record.Underline && existing.Strikethrough == record.Strikethrough {
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

type SyncDocumentResult struct {
	DocumentID catalog.DocumentID
	Path       string
}

// SyncDocument writes Markdown back to an existing active source file and
// refreshes its catalog/FTS observation. Annotation notes are separate Markdown
// documents and are reconciled by the annotation application flow.
func (s *Service) SyncDocument(ctx context.Context, selector, body string) (SyncDocumentResult, error) {
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return SyncDocumentResult{}, err
	}
	if document.Status != catalog.DocumentActive {
		return SyncDocumentResult{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
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
	if err := s.store.SaveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, Reindex: true}); err != nil {
		return SyncDocumentResult{}, err
	}
	return SyncDocumentResult{DocumentID: document.ID, Path: absolute}, nil
}

func (s *Service) ReindexDocument(ctx context.Context, selector string) error {
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
	return s.store.SaveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, Reindex: true})
}

func (s *Service) DeleteDocumentFile(ctx context.Context, selector string) (catalog.Document, string, error) {
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

// TrashDocumentFile soft-deletes a document: the Markdown file moves into the
// path root's hidden trash directory and a trash record hides the document
// from listings, search, graphs, and annotation DTOs. UUID, relations, index,
// and FTS entries stay intact so RestoreDocument is a plain move back.
func (s *Service) TrashDocumentFile(ctx context.Context, selector string) (catalog.Document, string, error) {
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
}

// RenameDocument moves an active source file to a new filename in the same
// directory and re-points the existing Document at the new location. The
// stable UUID, graph links, source URLs, and annotation relations survive
// untouched because only the location changes.
func (s *Service) RenameDocument(ctx context.Context, selector, newFilename string) (RenameDocumentResult, error) {
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
	if ext := strings.ToLower(filepath.Ext(newFilename)); ext != ".md" && ext != ".markdown" {
		return RenameDocumentResult{}, errors.New("renamed files must stay Markdown (.md or .markdown)")
	}
	if filepath.Base(absolute) == newFilename {
		return RenameDocumentResult{DocumentID: document.ID, Path: absolute}, nil
	}
	target := filepath.Join(filepath.Dir(absolute), newFilename)
	if _, err := os.Stat(target); err == nil {
		return RenameDocumentResult{}, fmt.Errorf("%s already exists", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return RenameDocumentResult{}, err
	}
	if err := s.writer.Move(ctx, absolute, target); err != nil {
		return RenameDocumentResult{}, err
	}
	relative := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(document.Location.RelativePath)), newFilename))
	document.Location.RelativePath = relative
	observation, err := s.scanner.ObserveFile(ctx, document.Location, target)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	if err := document.Observe(observation, s.clock.Now()); err != nil {
		return RenameDocumentResult{}, err
	}
	if err := s.store.SaveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, Reindex: true}); err != nil {
		return RenameDocumentResult{}, err
	}
	return RenameDocumentResult{DocumentID: document.ID, Path: target}, nil
}

func (s *Service) ListTopics(ctx context.Context) ([]port.DocumentRecord, error) {
	return s.store.ListTopics(ctx)
}

func (s *Service) ResolveTopicSelector(ctx context.Context, selector string) (*catalog.Document, string, error) {
	return s.store.ResolveTopic(ctx, strings.TrimSpace(selector))
}

type CreateNoteOptions struct {
	Title        string
	Body         string
	FromSelector string
	Topic        bool
	// Browser clip provenance (stored in document_sources, not inferred later).
	SourceURL string
	ClipMode  string // "selection" | "page" | ""
}

type CreateNoteResult struct {
	Document *catalog.Document
	Path     string
	Link     *catalog.GraphEdge
}

func (s *Service) CreateNote(ctx context.Context, opts CreateNoteOptions) (CreateNoteResult, error) {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return CreateNoteResult{}, errors.New("note title is required")
	}
	var from *catalog.Document
	if strings.TrimSpace(opts.FromSelector) != "" {
		resolved, _, err := s.ResolveDocument(ctx, opts.FromSelector)
		if err != nil {
			return CreateNoteResult{}, err
		}
		from = resolved
	}
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return CreateNoteResult{}, err
	}
	slug := noteSlug(title)
	if slug == "" {
		return CreateNoteResult{}, errors.New("note title does not produce a filename")
	}
	filename := slug + ".md"
	if opts.Topic {
		filename = "topic-" + slug + ".md"
		if existing, absolute, err := s.store.ResolveTopic(ctx, title); err == nil {
			return CreateNoteResult{Document: existing, Path: absolute}, nil
		}
	} else if strings.EqualFold(strings.TrimSpace(opts.ClipMode), "selection") {
		// Selection excerpts always use the *-note.md convention. A content
		// hash keeps names unique when two excerpts share the same slug
		// prefix, so the -2 collision suffix effectively never triggers.
		slug = strings.TrimSuffix(slug, "-note")
		if slug == "" {
			slug = "selection"
		}
		filename = slug + "-" + contentNameFragment(opts.Body, 10) + "-note.md"
	}
	absolute, err := s.availableNotePath(indexedPath.Root, filename)
	if err != nil {
		return CreateNoteResult{}, err
	}
	body := []byte(opts.Body)
	if len(body) == 0 {
		body = []byte("# " + title + "\n\n")
	}
	if err := s.writer.WriteNew(ctx, absolute, body); err != nil {
		return CreateNoteResult{}, err
	}
	report, err := s.scanOne(ctx, indexedPath)
	if err != nil {
		return CreateNoteResult{}, err
	}
	if report.Added == 0 {
		return CreateNoteResult{}, fmt.Errorf("created note %q was not indexed", absolute)
	}
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return CreateNoteResult{}, err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return CreateNoteResult{}, err
	}
	var created *catalog.Document
	for _, document := range documents {
		if document.Location.RelativePath == filepath.ToSlash(relative) && document.Status == catalog.DocumentActive {
			created = document
			break
		}
	}
	if created == nil {
		return CreateNoteResult{}, fmt.Errorf("created note %q was not indexed", absolute)
	}
	result := CreateNoteResult{Document: created, Path: absolute}
	if from != nil {
		edge, err := catalog.NewGraphEdge(from.ID, created.ID, catalog.EdgeManual, s.clock.Now())
		if err != nil {
			return CreateNoteResult{}, err
		}
		if _, err := s.store.AddEdge(ctx, edge); err != nil {
			return CreateNoteResult{}, err
		}
		result.Link = &edge
	}
	if source := strings.TrimSpace(opts.SourceURL); source != "" {
		mode := strings.TrimSpace(opts.ClipMode)
		if mode == "" && strings.HasSuffix(strings.ToLower(created.Location.RelativePath), "-note.md") {
			mode = "selection"
		}
		if mode == "" {
			mode = "page"
		}
		if err := s.store.UpsertDocumentSource(ctx, created.ID, source, mode, s.clock.Now()); err != nil {
			return CreateNoteResult{}, err
		}
	}
	return result, nil
}

// ListClipsBySourceURL returns documents registered for a normalized web URL.
// When selectionNotesOnly is true, only selection excerpts (*-note.md / clip_mode=selection) are returned.
func (s *Service) ListClipsBySourceURL(ctx context.Context, sourceURLNorm string, selectionNotesOnly bool) ([]port.DocumentSourceRecord, error) {
	return s.store.ListDocumentsBySourceURL(ctx, strings.TrimSpace(sourceURLNorm), selectionNotesOnly)
}

// ListDocumentSources enumerates browser-ingested documents, optionally
// filtered by clip mode.
func (s *Service) ListDocumentSources(ctx context.Context, clipMode string) ([]port.DocumentSourceRecord, error) {
	return s.store.ListDocumentSources(ctx, clipMode)
}

func (s *Service) defaultCreatePath(ctx context.Context) (*catalog.IndexedPath, error) {
	summaries, err := s.store.ListPaths(ctx, false)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, errors.New("no configured paths; add one with mm path add")
	}
	preferred, err := s.store.GetSetting(ctx, SettingMainPath)
	if err != nil {
		return nil, err
	}
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		for i := range summaries {
			if pathRootsEqual(summaries[i].Path.Root, preferred) {
				path := summaries[i].Path
				return &path, nil
			}
		}
	}
	// Default: first configured path (paths[0]).
	path := summaries[0].Path
	return &path, nil
}

func pathRootsEqual(a, b string) bool {
	left, err := filepath.Abs(filepath.Clean(a))
	if err != nil {
		left = filepath.Clean(a)
	}
	right, err := filepath.Abs(filepath.Clean(b))
	if err != nil {
		right = filepath.Clean(b)
	}
	return left == right
}

func (s *Service) availableNotePath(root, filename string) (string, error) {
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || filename == "" {
		return "", fmt.Errorf("invalid note filename %q", filename)
	}
	for index := 0; ; index++ {
		candidate := filename
		if index > 0 {
			extension := filepath.Ext(filename)
			candidate = strings.TrimSuffix(filename, extension) + "-" + strconv.Itoa(index+1) + extension
		}
		absolute := filepath.Join(root, candidate)
		if _, err := os.Stat(absolute); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return absolute, nil
	}
}

func noteSlug(title string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

// contentNameFragment derives a short fragment from the note body so
// selection notes sharing a slug prefix still get distinct filenames. It is
// deliberately alphabetic, never hex: document IDs are UUIDv7 hex strings, so
// a hex fragment in filenames would pollute UUID / short-ID searches.
func contentNameFragment(body string, length int) string {
	sum := sha256.Sum256([]byte(body))
	value := binary.BigEndian.Uint64(sum[:8])
	fragment := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		fragment[i] = "abcdefghijklmnopqrstuvwxyz"[value%26]
		value /= 26
	}
	return string(fragment)
}

func (s *Service) AddDocumentTopic(ctx context.Context, documentSelector, topicSelector string) (bool, error) {
	document, _, err := s.ResolveDocument(ctx, documentSelector)
	if err != nil {
		return false, err
	}
	topic, _, err := s.store.ResolveTopic(ctx, strings.TrimSpace(topicSelector))
	if err != nil {
		return false, err
	}
	edge, err := catalog.NewGraphEdge(document.ID, topic.ID, catalog.EdgeMember, s.clock.Now())
	if err != nil {
		return false, err
	}
	return s.store.AddEdge(ctx, edge)
}

func (s *Service) RemoveDocumentTopic(ctx context.Context, documentSelector, topicSelector string) (bool, error) {
	document, _, err := s.ResolveDocument(ctx, documentSelector)
	if err != nil {
		return false, err
	}
	topic, _, err := s.store.ResolveTopic(ctx, strings.TrimSpace(topicSelector))
	if err != nil {
		return false, err
	}
	return s.store.RemoveEdge(ctx, document.ID, topic.ID, catalog.EdgeMember)
}

type DocumentLinkResult struct {
	Created       bool
	AlreadyExists bool
}

func (s *Service) LinkDocuments(ctx context.Context, fromSelector, toSelector string) (DocumentLinkResult, error) {
	fromDocument, _, err := s.ResolveDocument(ctx, fromSelector)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	toDocument, _, err := s.ResolveDocument(ctx, toSelector)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	edge, err := catalog.NewGraphEdge(fromDocument.ID, toDocument.ID, catalog.EdgeManual, s.clock.Now())
	if err != nil {
		return DocumentLinkResult{}, err
	}
	created, err := s.store.AddEdge(ctx, edge)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	return DocumentLinkResult{Created: created, AlreadyExists: !created}, nil
}

func (s *Service) UnlinkDocuments(ctx context.Context, fromSelector, toSelector string) (bool, error) {
	fromDocument, _, err := s.ResolveDocument(ctx, fromSelector)
	if err != nil {
		return false, err
	}
	toDocument, _, err := s.ResolveDocument(ctx, toSelector)
	if err != nil {
		return false, err
	}
	return s.store.RemoveEdge(ctx, fromDocument.ID, toDocument.ID, catalog.EdgeManual)
}

func (s *Service) GetDocumentGraph(ctx context.Context, selector string) (*catalog.Document, catalog.DocumentGraph, error) {
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return nil, catalog.DocumentGraph{}, err
	}
	outgoing, incoming, topics, err := s.store.GetDocumentGraph(ctx, document.ID)
	if err != nil {
		return nil, catalog.DocumentGraph{}, err
	}
	graph := catalog.DocumentGraph{
		Outgoing: documentLinks(outgoing),
		Incoming: documentLinks(incoming),
		Topics:   documentLinks(topics),
	}
	return document, graph, nil
}

func documentLinks(records []port.DocumentRecord) []catalog.DocumentLink {
	links := make([]catalog.DocumentLink, 0, len(records))
	for _, record := range records {
		links = append(links, catalog.DocumentLink{Document: record.Document, Path: record.AbsolutePath})
	}
	return links
}

// NeighborhoodNode is one document discovered by the neighborhood walk, with
// the link distance from the focus document.
type NeighborhoodNode struct {
	Document *catalog.Document
	Path     string
	Distance int
}

// NeighborhoodEdge is a directed manual link between two discovered documents.
type NeighborhoodEdge struct {
	From catalog.DocumentID
	To   catalog.DocumentID
}

// Neighborhood is the document subgraph reachable within `depth` link hops.
type Neighborhood struct {
	Focus     *catalog.Document
	FocusPath string
	Depth     int
	Nodes     []NeighborhoodNode
	Edges     []NeighborhoodEdge
}

// GetDocumentNeighborhood walks the manual link graph outward from the focus
// document (links and backlinks alike) up to `depth` hops and returns every
// discovered document plus the edges between them. Depth < 1 is treated as 1.
func (s *Service) GetDocumentNeighborhood(ctx context.Context, selector string, depth int) (Neighborhood, error) {
	if depth < 1 {
		depth = 1
	}
	focus, focusPath, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return Neighborhood{}, err
	}
	nodes := map[catalog.DocumentID]NeighborhoodNode{focus.ID: {Document: focus, Path: focusPath, Distance: 0}}
	edgeSet := map[string]NeighborhoodEdge{}
	addEdge := func(from, to catalog.DocumentID) {
		key := string(from) + "\x00" + string(to)
		edgeSet[key] = NeighborhoodEdge{From: from, To: to}
	}
	frontier := []catalog.DocumentID{focus.ID}
	for distance := 1; distance <= depth && len(frontier) > 0; distance++ {
		var next []catalog.DocumentID
		for _, id := range frontier {
			outgoing, incoming, _, graphErr := s.store.GetDocumentGraph(ctx, id)
			if graphErr != nil {
				return Neighborhood{}, graphErr
			}
			for _, record := range outgoing {
				if record.Document == nil {
					continue
				}
				if _, seen := nodes[record.Document.ID]; !seen {
					nodes[record.Document.ID] = NeighborhoodNode{Document: record.Document, Path: record.AbsolutePath, Distance: distance}
					next = append(next, record.Document.ID)
				}
				addEdge(id, record.Document.ID)
			}
			for _, record := range incoming {
				if record.Document == nil {
					continue
				}
				if _, seen := nodes[record.Document.ID]; !seen {
					nodes[record.Document.ID] = NeighborhoodNode{Document: record.Document, Path: record.AbsolutePath, Distance: distance}
					next = append(next, record.Document.ID)
				}
				addEdge(record.Document.ID, id)
			}
		}
		frontier = next
	}
	result := Neighborhood{Focus: focus, FocusPath: focusPath, Depth: depth}
	for _, node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Distance != result.Nodes[j].Distance {
			return result.Nodes[i].Distance < result.Nodes[j].Distance
		}
		return result.Nodes[i].Path < result.Nodes[j].Path
	})
	for _, edge := range edgeSet {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})
	return result, nil
}

func (s *Service) ListTopicDocuments(ctx context.Context, selector string) (*catalog.Document, string, []catalog.DocumentLink, error) {
	topic, absolute, err := s.store.ResolveTopic(ctx, strings.TrimSpace(selector))
	if err != nil {
		return nil, "", nil, err
	}
	records, err := s.store.ListTopicDocuments(ctx, topic.ID)
	if err != nil {
		return nil, "", nil, err
	}
	return topic, absolute, documentLinks(records), nil
}

func (s *Service) Status(ctx context.Context) (port.StatusSnapshot, error) {
	return s.store.Status(ctx)
}

// Setting is one user-configurable option shown in the config panel.
type Setting struct {
	Key     string
	Label   string
	Value   string
	Options []string
}

// settingSpecs declares all configurable options. Adding a new setting only
// requires appending one entry here. Options may be empty for dynamic lists
// (see ListSettings).
var settingSpecs = []Setting{
	{Key: "viewer", Label: "viewer", Value: "leaf", Options: []string{"leaf", "web"}},
	{Key: "model", Label: "model", Value: "k3", Options: []string{"k3", "grok-4.5"}},
	{Key: SettingMainPath, Label: "main path", Value: "", Options: nil},
	{Key: SettingHideNotes, Label: "hide notes", Value: "on", Options: []string{"on", "off"}},
}

const (
	ViewerLeaf       = "leaf"
	ViewerWeb        = "web"
	SettingMainPath  = "main_path"
	SettingHideNotes = "hide_notes"
)

// GetViewer returns the configured viewer mode, defaulting to leaf.
func (s *Service) GetViewer(ctx context.Context) (string, error) {
	return s.getSetting(ctx, "viewer")
}

// SetViewer persists the viewer mode after validating it.
func (s *Service) SetViewer(ctx context.Context, viewer string) error {
	return s.SetSetting(ctx, "viewer", viewer)
}

// ListSettings returns every configurable option with its current value.
func (s *Service) ListSettings(ctx context.Context) ([]Setting, error) {
	out := make([]Setting, 0, len(settingSpecs))
	for _, spec := range settingSpecs {
		if spec.Key == SettingMainPath {
			setting, err := s.mainPathSetting(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, setting)
			continue
		}
		value, err := s.getSetting(ctx, spec.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, Setting{Key: spec.Key, Label: spec.Label, Value: value, Options: append([]string(nil), spec.Options...)})
	}
	return out, nil
}

func (s *Service) mainPathSetting(ctx context.Context) (Setting, error) {
	summaries, err := s.store.ListPaths(ctx, false)
	if err != nil {
		return Setting{}, err
	}
	options := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		options = append(options, summary.Path.Root)
	}
	value := ""
	if len(options) > 0 {
		value = options[0]
	}
	stored, err := s.store.GetSetting(ctx, SettingMainPath)
	if err != nil {
		return Setting{}, err
	}
	stored = strings.TrimSpace(stored)
	if stored != "" {
		for _, option := range options {
			if pathRootsEqual(option, stored) {
				value = option
				break
			}
		}
	}
	return Setting{
		Key:     SettingMainPath,
		Label:   "main path",
		Value:   value,
		Options: options,
	}, nil
}

// SetSetting validates and persists one configurable option.
func (s *Service) SetSetting(ctx context.Context, key, value string) error {
	spec, ok := findSettingSpec(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	if key == SettingMainPath {
		summaries, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return err
		}
		for _, summary := range summaries {
			if pathRootsEqual(summary.Path.Root, value) {
				return s.store.SetSetting(ctx, key, summary.Path.Root)
			}
		}
		return fmt.Errorf("main path %q is not a configured path; add it with mm path add", value)
	}
	valid := false
	for _, option := range spec.Options {
		if value == option {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid value %q for %q: use %s", value, key, strings.Join(spec.Options, " or "))
	}
	return s.store.SetSetting(ctx, key, value)
}

func (s *Service) getSetting(ctx context.Context, key string) (string, error) {
	spec, ok := findSettingSpec(key)
	if !ok {
		return "", fmt.Errorf("unknown setting %q", key)
	}
	if key == SettingMainPath {
		setting, err := s.mainPathSetting(ctx)
		if err != nil {
			return "", err
		}
		return setting.Value, nil
	}
	value, err := s.store.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if value == "" {
		return spec.Value, nil
	}
	valid := false
	for _, option := range spec.Options {
		if value == option {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("invalid value %q for setting %q", value, key)
	}
	return value, nil
}

func findSettingSpec(key string) (Setting, bool) {
	for _, spec := range settingSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return Setting{}, false
}

func (s *Service) resolvePath(ctx context.Context, selector string) (*catalog.IndexedPath, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, errors.New("path selector is required")
	}
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil && id > 0 {
		return s.store.ResolvePath(ctx, selector, false)
	}
	canonical, err := s.scanner.Canonicalize(selector)
	if err != nil {
		return nil, err
	}
	return s.store.ResolvePath(ctx, canonical, false)
}
