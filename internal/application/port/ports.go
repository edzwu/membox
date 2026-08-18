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

type DocumentScanner interface {
	Canonicalize(directory string) (string, error)
	Scan(ctx context.Context, indexedPath catalog.IndexedPath) (ScanResult, error)
	ObserveFile(
		ctx context.Context,
		location catalog.Location,
		absolutePath string,
	) (catalog.Observation, error)
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
	NewResourceID() (string, error)
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
	ReadStatus   string
}

type GraphStore interface {
	ResolveTopic(ctx context.Context, selector string) (*catalog.Document, string, error)
	ListTopics(ctx context.Context) ([]DocumentRecord, error)
	AddEdge(ctx context.Context, edge catalog.GraphEdge) (created bool, err error)
	RemoveEdge(
		ctx context.Context,
		fromDocumentID, toDocumentID catalog.DocumentID,
		kind catalog.EdgeKind,
	) (removed bool, err error)
	GetDocumentGraph(
		ctx context.Context,
		documentID catalog.DocumentID,
	) (outgoing []DocumentRecord, incoming []DocumentRecord, topics []DocumentRecord, err error)
	ListTopicDocuments(
		ctx context.Context,
		topicDocumentID catalog.DocumentID,
	) ([]DocumentRecord, error)
}

// ContentObject identifies immutable bytes published before catalog metadata
// is committed. SHA256 is the logical whole-content hash and ObjectHash is the
// physical object name; they are equal for the initial direct representation.
type ContentObject struct {
	SHA256     string
	ObjectHash string
	Size       int64
}

// ImmutableContentStore publishes content-addressed bytes. Implementations
// must make a successful Put durable and visible before returning so SQLite
// can safely commit references afterward. Existing objects are deduplicated.
type ImmutableContentStore interface {
	Put(ctx context.Context, body []byte) (ContentObject, error)
}

type ScanSave struct {
	Document   *catalog.Document
	Body       []byte // authoritative source bytes (used for immutable versions)
	SearchText []byte // rebuildable text projection (Markdown body or extracted PDF text)
	Reindex    bool
	// Content is set only after Body has been published to immutable storage.
	// SaveScan/SaveDocument atomically create a Version and advance the head
	// when this content differs from the current head.
	Content *ContentObject
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
	// Kind distinguishes note subtypes in the DB (empty = plain selection note,
	// "qa" = assist Q&A). Mirrored in note Markdown front matter as kind:.
	Kind      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type DocumentReadState struct {
	DocumentID catalog.DocumentID
	ProgressY  int
	ProgressAt string
	// ReadStatus is the semantic reading state: "unread", "reading", "finished".
	ReadStatus string
	FinishedAt time.Time
}

// ReviewCard joins an annotation note with its source document and spaced-
// review schedule. Produced by the store for the review feed.
type ReviewCard struct {
	NoteDocumentID   string
	TargetDocumentID string
	Kind             string
	Highlight        bool
	Underline        bool
	Strikethrough    bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Schedule         CardSchedule
}

// CardSchedule is the spaced-review state for one note card.
type CardSchedule struct {
	NoteDocumentID string
	DueAt          int64 // ms epoch; 0 = new card
	IntervalDays   int64
	Ease           float64
	Reps           int64
	Lapses         int64
	LastReviewedAt int64 // ms epoch; 0 = never
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
	TrashDocument(
		ctx context.Context,
		documentID catalog.DocumentID,
		originRelativePath string,
		trashedAt time.Time,
	) error
	GetTrashedDocument(
		ctx context.Context,
		documentID catalog.DocumentID,
	) (TrashRecord, bool, error)
	DeleteTrashRecord(ctx context.Context, documentID catalog.DocumentID) error
	ListTrashedDocuments(ctx context.Context) ([]DocumentRecord, []TrashRecord, error)
	SetDocumentRelativePath(
		ctx context.Context,
		documentID catalog.DocumentID,
		relativePath string,
	) error
	PurgeDocument(ctx context.Context, documentID catalog.DocumentID) error
}

// CatalogStore is an outbound persistence/read-model port. SaveScan must save
// 负责保存文档的结构化元数据
// all aggregate and FTS changes atomically.
type CatalogStore interface {
	Close() error
	AddOrReactivatePath(
		ctx context.Context,
		canonicalRoot string,
		now time.Time,
	) (*catalog.IndexedPath, bool, error)
	ListPaths(ctx context.Context, includeRemoved bool) ([]PathSummary, error)
	ResolvePath(
		ctx context.Context,
		selector string,
		includeRemoved bool,
	) (*catalog.IndexedPath, error)
	RemovePath(ctx context.Context, id catalog.IndexedPathID, now time.Time) (int, error)
	DocumentsForPath(ctx context.Context, id catalog.IndexedPathID) ([]*catalog.Document, error)
	SaveScan(ctx context.Context, indexedPath *catalog.IndexedPath, saves []ScanSave) error
	SaveDocument(ctx context.Context, save ScanSave) error
	SaveSourceTimes(ctx context.Context, documents []*catalog.Document) error
	SavePinned(ctx context.Context, documentID catalog.DocumentID, pinned bool) error
	// SetDocumentSummary stores the document's summary; scans preserve it.
	SetDocumentSummary(ctx context.Context, documentID catalog.DocumentID, summary string) error
	// SaveAnnotations/GetAnnotations retain legacy Miru sidecars only for
	// migration. New annotation content is stored as Markdown note documents.
	SaveAnnotations(ctx context.Context, documentID catalog.DocumentID, sidecar string) error
	GetAnnotations(ctx context.Context, documentID catalog.DocumentID) (string, error)
	UpsertAnnotationNote(ctx context.Context, record AnnotationNoteRecord) error
	ListAnnotationNotes(
		ctx context.Context,
		targetDocumentID catalog.DocumentID,
	) ([]AnnotationNoteRecord, error)
	GetAnnotationNote(
		ctx context.Context,
		noteDocumentID catalog.DocumentID,
	) (AnnotationNoteRecord, bool, error)
	DeleteAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) error
	// Review queue: all active note cards + spaced-review schedules.
	ListReviewCards(ctx context.Context) ([]ReviewCard, error)
	SaveCardSchedule(ctx context.Context, schedule CardSchedule) error
	ListScheduledCardIDs(ctx context.Context) (map[string]bool, error)
	SaveDocumentReadState(ctx context.Context, state DocumentReadState) error
	GetDocumentReadState(
		ctx context.Context,
		documentID catalog.DocumentID,
	) (DocumentReadState, bool, error)
	ListRecentDocuments(ctx context.Context, limit int) ([]RecentDocument, error)
	ListRecentlyModifiedDocuments(ctx context.Context, limit int) ([]ModifiedDocument, error)
	Search(ctx context.Context, query string, limit int, exact bool) ([]SearchHit, error)
	GrepDocuments(ctx context.Context, pattern string, limit int) ([]SearchHit, error)
	SuggestDocuments(ctx context.Context, query string, limit int) ([]SearchHit, error)
	SetDocumentReadStatus(
		ctx context.Context,
		documentID catalog.DocumentID,
		status string,
		finishedAt time.Time,
	) error
	ListDocuments(
		ctx context.Context,
		limit int,
		includeUnavailable bool,
		statusFilter string,
	) ([]DocumentRecord, error)
	ResolveDocument(ctx context.Context, selector string) (*catalog.Document, string, error)
	// Document source URLs (browser clip provenance), stored at ingest time.
	UpsertDocumentSource(
		ctx context.Context,
		documentID catalog.DocumentID,
		sourceURLNorm, clipMode string,
		now time.Time,
	) error
	ListDocumentsBySourceURL(
		ctx context.Context,
		sourceURLNorm string,
		selectionNotesOnly bool,
	) ([]DocumentSourceRecord, error)
	// ListDocumentSources enumerates documents with clip provenance, optionally
	// filtered by clip mode ("page" | "selection" | "" for all).
	ListDocumentSources(ctx context.Context, clipMode string) ([]DocumentSourceRecord, error)
	// URL resources are owned by membox, not by planning/task databases.
	IngestResources(ctx context.Context, resources []ResourceInsert) ([]ResourceIngestRecord, error)
	AssessResources(ctx context.Context, assessments []ResourceAssessment) ([]ResourceRecord, error)
	ListResources(ctx context.Context, limit int) ([]ResourceRecord, error)
	ListResourcesBySource(ctx context.Context, sourceDocumentID, sourceFile string, limit int) ([]ResourceRecord, error)
	ResourceScanState(ctx context.Context, source string) (int, error)
	SetResourceScanState(ctx context.Context, source string, wave int, reset bool, now time.Time) error
	GraphStore
	TrashStore
	Status(ctx context.Context) (StatusSnapshot, error)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	// Questions exposes the personal question accumulation store.
	Questions() QuestionStore
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

// ResourceRecord is a canonical external URL captured from a membox document.
// The source document/file fields are provenance; URL identity is canonical_url.
type ResourceRecord struct {
	ID               string    `json:"id"`
	URL              string    `json:"url"`
	CanonicalURL     string    `json:"canonical_url"`
	Title            string    `json:"title,omitempty"`
	Priority         string    `json:"priority"`
	Score            *float64  `json:"score,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	SourceDocumentID string    `json:"source_document_id,omitempty"`
	SourceFile       string    `json:"source_file,omitempty"`
	SourceLine       string    `json:"source_line,omitempty"`
	SourceCommit     string    `json:"source_commit,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ResourceInsert struct {
	ID               string
	URL              string
	CanonicalURL     string
	Title            string
	SourceDocumentID string
	SourceFile       string
	SourceLine       string
	SourceCommit     string
	CreatedAt        time.Time
}

type ResourceIngestRecord struct {
	Resource ResourceRecord
	Inserted bool
}

type ResourceAssessment struct {
	ID        string  `json:"id"`
	Priority  string  `json:"priority"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	UpdatedAt time.Time
}

// Question accumulation: personal open questions with optional answers,
// linked to the document they arose from. Persisted in the same SQLite DB.
// CanonicalBody is the stable dedupe key (like resources.canonical_url).
type Question struct {
	ID               string    `json:"id"`
	Body             string    `json:"body"`
	CanonicalBody    string    `json:"canonical_body,omitempty"`
	Status           string    `json:"status"` // QuestionOpen | QuestionAnswered | QuestionArchived
	Answer           string    `json:"answer,omitempty"`
	SourceDocumentID string    `json:"source_document_id,omitempty"`
	SourceFile       string    `json:"source_file,omitempty"`
	SourceLine       string    `json:"source_line,omitempty"`
	SourceCommit     string    `json:"source_commit,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	AnsweredAt       time.Time `json:"answered_at,omitempty"`
}

const (
	QuestionOpen     = "open"
	QuestionAnswered = "answered"
	QuestionArchived = "archived"
)

type QuestionListQuery struct {
	Status           string
	SourceDocumentID string
	SourceFile       string
	Limit            int
}

type QuestionInsert struct {
	ID               string
	Body             string
	CanonicalBody    string
	SourceDocumentID string
	SourceFile       string
	SourceLine       string
	SourceCommit     string
	CreatedAt        time.Time
}

type QuestionIngestRecord struct {
	Question Question
	Inserted bool
}

// QuestionStore persists questions. Resolve accepts the full ID or a unique
// short suffix; Delete requires a full ID to avoid accidental suffix matches.
// Ingest is the idempotent inbox-scan path (canonical_body dedupe).
type QuestionStore interface {
	Add(ctx context.Context, q Question) (Question, error)
	Ingest(ctx context.Context, questions []QuestionInsert) ([]QuestionIngestRecord, error)
	List(ctx context.Context, query QuestionListQuery) ([]Question, error)
	Resolve(ctx context.Context, selector string) (Question, error)
	Update(ctx context.Context, q Question) (Question, error)
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (map[string]int, error)
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
