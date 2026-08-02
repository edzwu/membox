package membox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"membox/internal/application"
	"membox/internal/application/port"
	"membox/internal/bootstrap"
	"membox/internal/domain/catalog"
)

type Config struct {
	Home         string
	DatabasePath string
}

func DefaultConfig() (Config, error) {
	home := os.Getenv("MEMBOX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		home = filepath.Join(userHome, ".membox")
	}
	return Config{Home: home, DatabasePath: filepath.Join(home, "membox.db")}, nil
}

type Box struct{ service *application.Service }

func Open(config Config) (*Box, error) {
	if config.DatabasePath == "" {
		if config.Home == "" {
			return nil, errors.New("membox home or database path is required")
		}
		config.DatabasePath = filepath.Join(config.Home, "membox.db")
	}
	service, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		return nil, err
	}
	return &Box{service: service}, nil
}

func (b *Box) Close() error { return b.service.Close() }

type PathView struct {
	ID         int64      `json:"id"`
	Path       string     `json:"path"`
	Documents  int        `json:"documents"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	LastScanAt *time.Time `json:"last_scan_at"`
	LastError  string     `json:"last_error,omitempty"`
}

type AddPathCommand struct{ Directory string }
type AddPathResult struct {
	Path          PathView   `json:"path"`
	AlreadyExists bool       `json:"already_exists"`
	Scan          ScanReport `json:"scan"`
}

type RemovePathCommand struct{ Selector string }
type RemovePathResult struct {
	PathID    int64 `json:"path_id"`
	Documents int   `json:"documents"`
}

type ScanPathsCommand struct {
	Selector        string
	TimestampSource string
}
type ScanReport struct {
	Paths               int    `json:"paths"`
	Files               int    `json:"files"`
	Added               int    `json:"added"`
	Updated             int    `json:"updated"`
	Renamed             int    `json:"renamed"`
	Unchanged           int    `json:"unchanged"`
	Missing             int    `json:"missing"`
	PossibleRenames     int    `json:"possible_renames"`
	Errors              int    `json:"errors"`
	TimestampSource     string `json:"timestamp_source,omitempty"`
	GitPaths            int    `json:"git_paths,omitempty"`
	TimestampsUpdated   int    `json:"timestamps_updated,omitempty"`
	TimestampsUnchanged int    `json:"timestamps_unchanged,omitempty"`
	NoGitHistory        int    `json:"no_git_history,omitempty"`
	NonGitPaths         int    `json:"non_git_paths,omitempty"`
}

func (b *Box) AddPath(ctx context.Context, command AddPathCommand) (AddPathResult, error) {
	result, err := b.service.AddPath(ctx, command.Directory)
	return AddPathResult{Path: pathView(result.Path), AlreadyExists: result.AlreadyExists, Scan: scanReport(result.Scan)}, err
}

func (b *Box) ListPaths(ctx context.Context) ([]PathView, error) {
	paths, err := b.service.ListPaths(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PathView, 0, len(paths))
	for _, path := range paths {
		out = append(out, pathView(path))
	}
	return out, nil
}

func (b *Box) RemovePath(ctx context.Context, command RemovePathCommand) (RemovePathResult, error) {
	result, err := b.service.RemovePath(ctx, command.Selector)
	return RemovePathResult{PathID: int64(result.Path.ID), Documents: result.Documents}, err
}

func (b *Box) ScanPaths(ctx context.Context, command ScanPathsCommand) (ScanReport, error) {
	report, err := b.service.ScanPaths(ctx, application.ScanOptions{Selector: command.Selector, TimestampSource: command.TimestampSource})
	return scanReport(report), err
}

type SearchDocumentsQuery struct {
	Query string
	Limit int
}
type SearchResult struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Snippet    string `json:"snippet"`
}

func (b *Box) SearchDocuments(ctx context.Context, query SearchDocumentsQuery) ([]SearchResult, error) {
	hits, err := b.service.Search(ctx, query.Query, query.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(hits))
	for _, hit := range hits {
		out = append(out, SearchResult{DocumentID: string(hit.DocumentID), Title: hit.Title, Path: hit.Path, Snippet: hit.Snippet})
	}
	return out, nil
}

type ListDocumentsQuery struct {
	Limit int
	All   bool
}

type GetDocumentQuery struct{ Selector string }
type DocumentView struct {
	ID           string     `json:"id"`
	Path         string     `json:"path"`
	PathID       int64      `json:"path_id"`
	RelativePath string     `json:"relative_path"`
	Status       string     `json:"status"`
	Pinned       bool       `json:"pinned"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary"`
	MTime        int64      `json:"mtime"`
	Size         int64      `json:"size"`
	SHA256       string     `json:"sha256"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	IndexedAt    *time.Time `json:"indexed_at"`
}

func (b *Box) ListDocuments(ctx context.Context, query ListDocumentsQuery) ([]DocumentView, error) {
	records, err := b.service.ListDocuments(ctx, query.Limit, query.All)
	if err != nil {
		return nil, err
	}
	views := make([]DocumentView, 0, len(records))
	for _, record := range records {
		views = append(views, documentView(record.Document, record.AbsolutePath))
	}
	return views, nil
}

func (b *Box) GetDocument(ctx context.Context, query GetDocumentQuery) (DocumentView, error) {
	document, path, err := b.service.ResolveDocument(ctx, query.Selector)
	if err != nil {
		return DocumentView{}, err
	}
	return documentView(document, path), nil
}

func documentView(document *catalog.Document, path string) DocumentView {
	view := DocumentView{ID: string(document.ID), Path: path, PathID: int64(document.Location.PathID), RelativePath: document.Location.RelativePath,
		Status: string(document.Status), Pinned: document.Pinned, Title: document.Index.Title, Summary: document.Index.Summary, MTime: document.Index.MTime, Size: document.Index.Size,
		SHA256: document.Index.SHA256, CreatedAt: document.Index.SourceCreatedAt, UpdatedAt: document.Index.SourceUpdatedAt}
	if !document.Index.IndexedAt.IsZero() {
		value := document.Index.IndexedAt
		view.IndexedAt = &value
	}
	return view
}

type ResolveLocationQuery struct{ Selector string }
type LocationView struct{ DocumentID, Path, Status string }

func (b *Box) ResolveDocumentLocation(ctx context.Context, query ResolveLocationQuery) (LocationView, error) {
	document, path, err := b.service.ResolveDocument(ctx, query.Selector)
	if err != nil {
		return LocationView{}, err
	}
	return LocationView{DocumentID: string(document.ID), Path: path, Status: string(document.Status)}, nil
}

type ReadDocumentQuery struct{ Selector string }

func (b *Box) ReadDocument(ctx context.Context, query ReadDocumentQuery) ([]byte, error) {
	return b.service.ReadDocument(ctx, query.Selector)
}

type ToggleDocumentPinCommand struct{ Selector string }
type ToggleDocumentPinResult struct {
	DocumentID string `json:"document_id"`
	Pinned     bool   `json:"pinned"`
}

func (b *Box) ToggleDocumentPin(ctx context.Context, command ToggleDocumentPinCommand) (ToggleDocumentPinResult, error) {
	result, err := b.service.ToggleDocumentPin(ctx, command.Selector)
	return ToggleDocumentPinResult{DocumentID: string(result.DocumentID), Pinned: result.Pinned}, err
}

type ReindexDocumentCommand struct{ Selector string }

func (b *Box) ReindexDocument(ctx context.Context, command ReindexDocumentCommand) error {
	return b.service.ReindexDocument(ctx, command.Selector)
}

type IndexStatusView struct {
	Paths        int        `json:"paths"`
	Active       int        `json:"active"`
	Missing      int        `json:"missing"`
	Untracked    int        `json:"untracked"`
	LastScanAt   *time.Time `json:"last_scan_at"`
	DatabasePath string     `json:"database_path"`
}

func (b *Box) GetIndexStatus(ctx context.Context) (IndexStatusView, error) {
	status, err := b.service.Status(ctx)
	return IndexStatusView{Paths: status.Paths, Active: status.Active, Missing: status.Missing, Untracked: status.Untracked, LastScanAt: status.LastScanAt, DatabasePath: status.DatabasePath}, err
}

func pathView(summary port.PathSummary) PathView {
	return PathView{ID: int64(summary.Path.ID), Path: summary.Path.Root, Documents: summary.DocumentCount,
		Status: string(summary.Path.Status), CreatedAt: summary.Path.CreatedAt, LastScanAt: summary.Path.LastScanAt,
		LastError: summary.Path.LastError}
}

func scanReport(report application.ScanReport) ScanReport {
	return ScanReport{Paths: report.Paths, Files: report.Files, Added: report.Added, Updated: report.Updated,
		Renamed: report.Renamed, Unchanged: report.Unchanged, Missing: report.Missing,
		PossibleRenames: report.PossibleRenames, Errors: report.Errors, TimestampSource: report.TimestampSource,
		GitPaths: report.GitPaths, TimestampsUpdated: report.TimestampsUpdated, TimestampsUnchanged: report.TimestampsUnchanged,
		NoGitHistory: report.NoGitHistory, NonGitPaths: report.NonGitPaths}
}
