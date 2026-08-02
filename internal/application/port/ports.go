package port

import (
	"context"
	"io"
	"time"

	"membox/internal/domain/catalog"
)

type ScanIssue struct {
	Path string
	Err  error
}

type ScanResult struct {
	Observations []catalog.Observation
	Issues       []ScanIssue
}

type MarkdownScanner interface {
	Canonicalize(directory string) (string, error)
	Scan(ctx context.Context, indexedPath catalog.IndexedPath) (ScanResult, error)
	ObserveFile(ctx context.Context, location catalog.Location, absolutePath string) (catalog.Observation, error)
}

type ContentReader interface {
	Read(ctx context.Context, absolutePath string) ([]byte, error)
}

type IDGenerator interface {
	NewDocumentID() (catalog.DocumentID, error)
}

type Clock interface{ Now() time.Time }

type RevisionTimes struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GitHistory reads source timestamps without exposing process execution to the
// application layer. RepositoryRoot returns applicable=false for non-Git paths.
type GitHistory interface {
	RepositoryRoot(ctx context.Context, directory string) (root string, applicable bool, err error)
	FileTimes(ctx context.Context, repositoryRoot, absolutePath string) (RevisionTimes, bool, error)
}

type PathSummary struct {
	Path          catalog.IndexedPath
	DocumentCount int
}

type SearchHit struct {
	DocumentID catalog.DocumentID
	Title      string
	Path       string
	Snippet    string
}

type DocumentRecord struct {
	Document     *catalog.Document
	AbsolutePath string
}

type ScanSave struct {
	Document *catalog.Document
	Body     []byte
	Reindex  bool
}

// CatalogStore is an outbound persistence/read-model port. SaveScan must save
// all aggregate and FTS changes atomically.
type CatalogStore interface {
	Close() error
	AddOrReactivatePath(ctx context.Context, canonicalRoot string, now time.Time) (*catalog.IndexedPath, bool, error)
	ListPaths(ctx context.Context, includeRemoved bool) ([]PathSummary, error)
	ResolvePath(ctx context.Context, selector string, includeRemoved bool) (*catalog.IndexedPath, error)
	RemovePath(ctx context.Context, id catalog.IndexedPathID, now time.Time) (int, error)
	DocumentsForPath(ctx context.Context, id catalog.IndexedPathID) ([]*catalog.Document, error)
	SaveScan(ctx context.Context, indexedPath *catalog.IndexedPath, saves []ScanSave) error
	SaveDocument(ctx context.Context, save ScanSave) error
	SaveSourceTimes(ctx context.Context, documents []*catalog.Document) error
	SavePinned(ctx context.Context, documentID catalog.DocumentID, pinned bool) error
	Search(ctx context.Context, query string, limit int) ([]SearchHit, error)
	ListDocuments(ctx context.Context, limit int, includeUnavailable bool) ([]DocumentRecord, error)
	ResolveDocument(ctx context.Context, selector string) (*catalog.Document, string, error)
	Status(ctx context.Context) (StatusSnapshot, error)
}

type StatusSnapshot struct {
	Paths        int
	Active       int
	Missing      int
	Untracked    int
	LastScanAt   *time.Time
	DatabasePath string
}

// ReaderFactory is reserved for streaming reads without exposing filesystem
// details to application callers.
type ReaderFactory interface {
	Open(ctx context.Context, absolutePath string) (io.ReadCloser, error)
}
