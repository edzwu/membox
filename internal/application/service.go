package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	return result, nil
}

func (s *Service) defaultCreatePath(ctx context.Context) (*catalog.IndexedPath, error) {
	summaries, err := s.store.ListPaths(ctx, false)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, errors.New("no configured paths; add one with mm path add")
	}
	path := summaries[0].Path
	return &path, nil
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
// requires appending one entry here.
var settingSpecs = []Setting{
	{Key: "viewer", Label: "viewer", Value: "leaf", Options: []string{"leaf", "web"}},
	{Key: "model", Label: "model", Value: "k3", Options: []string{"k3", "grok-4.5"}},
}

const (
	ViewerLeaf = "leaf"
	ViewerWeb  = "web"
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
		value, err := s.getSetting(ctx, spec.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, Setting{Key: spec.Key, Label: spec.Label, Value: value, Options: spec.Options})
	}
	return out, nil
}

// SetSetting validates and persists one configurable option.
func (s *Service) SetSetting(ctx context.Context, key, value string) error {
	spec, ok := findSettingSpec(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
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
