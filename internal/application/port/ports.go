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

type ContentWriter interface {
	WriteNew(ctx context.Context, absolutePath string, body []byte) error
	Write(ctx context.Context, absolutePath string, body []byte) error
	Remove(ctx context.Context, absolutePath string) error
	Move(ctx context.Context, fromPath, toPath string) error
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

type GraphStore interface {
	ResolveTopic(ctx context.Context, selector string) (*catalog.Document, string, error)
	ListTopics(ctx context.Context) ([]DocumentRecord, error)
	AddEdge(ctx context.Context, edge catalog.GraphEdge) (created bool, err error)
	RemoveEdge(ctx context.Context, fromDocumentID, toDocumentID catalog.DocumentID, kind catalog.EdgeKind) (removed bool, err error)
	GetDocumentGraph(ctx context.Context, documentID catalog.DocumentID) (outgoing []DocumentRecord, incoming []DocumentRecord, topics []DocumentRecord, err error)
	ListTopicDocuments(ctx context.Context, topicDocumentID catalog.DocumentID) ([]DocumentRecord, error)
}

type ScanSave struct {
	Document *catalog.Document
	Body     []byte
	Reindex  bool
}

// AnnotationNoteRecord links one Markdown note document to the document
// passage it annotates. User-authored excerpt/note text stays in Markdown;
// this row only stores identity, anchoring hints, and presentation metadata.
type AnnotationNoteRecord struct {
	NoteDocumentID   catalog.DocumentID
	TargetDocumentID catalog.DocumentID
	Start            int
	Prefix           string
	Suffix           string
	Highlight        bool
	Underline        bool
	Strikethrough    bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type DocumentReadState struct {
	DocumentID catalog.DocumentID
	ProgressY  int
	ProgressAt string
}

// RecentDocument is a compact picker-oriented view ordered by the last time a
// document was opened in the reader.
type RecentDocument struct {
	DocumentID catalog.DocumentID
	Title      string
	Path       string
	OpenedAt   string
}

// ModifiedDocument is a compact picker-oriented view ordered by source or
// annotation modification time, newest first.
type ModifiedDocument struct {
	DocumentID catalog.DocumentID
	Title      string
	Path       string
	ModifiedAt time.Time
}

// MutationLocker serializes document mutations across every process that
// shares one membox home (TUI, CLI, Web Companion). Implementations must be
// reentrant per goroutine so composed service operations lock once.
type MutationLocker interface {
	Lock() error
	Unlock() error
	Close() error
}

// TrashRecord marks a document as soft-deleted. Rows and relations survive;
// the file sits in the path's trash directory until restore or purge.
type TrashRecord struct {
	DocumentID         catalog.DocumentID
	OriginRelativePath string
	TrashedAt          time.Time
}

type TrashStore interface {
	TrashDocument(ctx context.Context, documentID catalog.DocumentID, originRelativePath string, trashedAt time.Time) error
	GetTrashedDocument(ctx context.Context, documentID catalog.DocumentID) (TrashRecord, bool, error)
	DeleteTrashRecord(ctx context.Context, documentID catalog.DocumentID) error
	ListTrashedDocuments(ctx context.Context) ([]DocumentRecord, []TrashRecord, error)
	SetDocumentRelativePath(ctx context.Context, documentID catalog.DocumentID, relativePath string) error
	PurgeDocument(ctx context.Context, documentID catalog.DocumentID) error
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
	// SaveAnnotations/GetAnnotations retain legacy Miru sidecars only for
	// migration. New annotation content is stored as Markdown note documents.
	SaveAnnotations(ctx context.Context, documentID catalog.DocumentID, sidecar string) error
	GetAnnotations(ctx context.Context, documentID catalog.DocumentID) (string, error)
	UpsertAnnotationNote(ctx context.Context, record AnnotationNoteRecord) error
	ListAnnotationNotes(ctx context.Context, targetDocumentID catalog.DocumentID) ([]AnnotationNoteRecord, error)
	GetAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) (AnnotationNoteRecord, bool, error)
	DeleteAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) error
	SaveDocumentReadState(ctx context.Context, state DocumentReadState) error
	GetDocumentReadState(ctx context.Context, documentID catalog.DocumentID) (DocumentReadState, bool, error)
	ListRecentDocuments(ctx context.Context, limit int) ([]RecentDocument, error)
	ListRecentlyModifiedDocuments(ctx context.Context, limit int) ([]ModifiedDocument, error)
	Search(ctx context.Context, query string, limit int, exact bool) ([]SearchHit, error)
	SuggestDocuments(ctx context.Context, query string, limit int) ([]SearchHit, error)
	ListDocuments(ctx context.Context, limit int, includeUnavailable bool) ([]DocumentRecord, error)
	ResolveDocument(ctx context.Context, selector string) (*catalog.Document, string, error)
	// Document source URLs (browser clip provenance), stored at ingest time.
	UpsertDocumentSource(ctx context.Context, documentID catalog.DocumentID, sourceURLNorm, clipMode string, now time.Time) error
	ListDocumentsBySourceURL(ctx context.Context, sourceURLNorm string, selectionNotesOnly bool) ([]DocumentSourceRecord, error)
	// ListDocumentSources enumerates documents with clip provenance, optionally
	// filtered by clip mode ("page" | "selection" | "" for all).
	ListDocumentSources(ctx context.Context, clipMode string) ([]DocumentSourceRecord, error)
	GraphStore
	TrashStore
	Status(ctx context.Context) (StatusSnapshot, error)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// DocumentSourceRecord is a document that was ingested from a web URL.
type DocumentSourceRecord struct {
	DocumentID   catalog.DocumentID
	Title        string
	AbsolutePath string
	RelativePath string
	ClipMode     string // "selection" | "page" | ""
	SourceURL    string
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
