package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

type Service struct {
	store    port.CatalogStore
	scanner  port.MarkdownScanner
	reader   port.ContentReader
	writer   port.ContentWriter
	ids      port.IDGenerator
	clock    port.Clock
	history  port.GitHistory
	contents port.ImmutableContentStore
	// mutations serializes every mutating operation across all processes
	// sharing this membox home. Nil in unit tests without a home directory.
	mutations port.MutationLocker
}

func NewService(store port.CatalogStore, scanner port.MarkdownScanner, reader port.ContentReader, writer port.ContentWriter, ids port.IDGenerator, clock port.Clock, history port.GitHistory) *Service {
	return &Service{store: store, scanner: scanner, reader: reader, writer: writer, ids: ids, clock: clock, history: history}
}

// SetMutationLocker installs the cross-process mutation lock. Bootstrap wires
// it immediately after construction; callers that never share the home (unit
// tests) can skip it.
func (s *Service) SetMutationLocker(locker port.MutationLocker) { s.mutations = locker }

// SetContentStore enables immutable content/version capture. It is wired by
// mmd; legacy in-process clients can continue operating during migration.
func (s *Service) SetContentStore(store port.ImmutableContentStore) { s.contents = store }

// Store returns the outbound catalog port. Used by composition roots (Companion)
// to access optional store capabilities such as the Agent session catalog.
func (s *Service) Store() port.CatalogStore { return s.store }

func (s *Service) prepareSaveContent(ctx context.Context, save *port.ScanSave) error {
	if s.contents == nil || save == nil || !save.Reindex {
		return nil
	}
	if save.Document == nil {
		return errors.New("cannot publish content for nil document")
	}
	object, err := s.contents.Put(ctx, save.Body)
	if err != nil {
		return fmt.Errorf("publish content for document %s: %w", save.Document.ID, err)
	}
	if object.SHA256 != save.Document.Index.SHA256 || object.Size != save.Document.Index.Size {
		return fmt.Errorf("published content does not match document %s index", save.Document.ID)
	}
	save.Content = &object
	return nil
}

func (s *Service) prepareScanContent(ctx context.Context, saves []port.ScanSave) error {
	for i := range saves {
		if err := s.prepareSaveContent(ctx, &saves[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) saveDocument(ctx context.Context, save port.ScanSave) error {
	if err := s.prepareSaveContent(ctx, &save); err != nil {
		return err
	}
	return s.store.SaveDocument(ctx, save)
}

func (s *Service) Close() error {
	var err error
	if s.mutations != nil {
		err = s.mutations.Close()
	}
	return errors.Join(s.store.Close(), err)
}

// beginMutation takes the cross-process mutation lock; the returned release
// function must be called exactly once. The lock is reentrant per goroutine,
// so composed operations (e.g. SaveAnnotationNote → CreateNote) serialize
// once for the whole resolve → file write → observe → commit sequence.
func (s *Service) beginMutation() (func(), error) {
	if s.mutations == nil {
		return func() {}, nil
	}
	if err := s.mutations.Lock(); err != nil {
		return nil, err
	}
	return func() { _ = s.mutations.Unlock() }, nil
}

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
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return AddPathResult{}, lockErr
	}
	defer release()
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
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return RemovePathResult{}, lockErr
	}
	defer release()
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
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return ScanReport{}, lockErr
	}
	defer release()
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
	// Publish immutable objects before the SQLite transaction. A crash can
	// leave an unreferenced object for later GC, but can never commit a Version
	// whose bytes are missing.
	if err := s.prepareScanContent(ctx, saves); err != nil {
		return report, err
	}
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
