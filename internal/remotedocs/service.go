// Package remotedocs is the remote node's document service (plan M4):
// owner-scoped CRUD with immutable version snapshots, content-addressed R2
// blobs, and If-Match concurrency. Handlers stay thin; all rules live here.
package remotedocs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var (
	ErrNotFound         = errors.New("document not found")
	ErrRevisionConflict = errors.New("revision conflict")
	ErrUnsupportedType  = errors.New("only PDF and Markdown are supported")
)

// Operations recorded in document_versions (plan T09).
const (
	OpCreate        = "create"
	OpUpdateMeta    = "update_meta"
	OpUpdateContent = "update_content"
	OpDelete        = "delete"
)

type Document struct {
	ID              string
	OwnerID         string
	CurrentRevision int
	Title           string
	SourceURL       string
	Deleted         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Version is one immutable snapshot. Blob fields are empty for metadata-only
// revisions and tombstones.
type Version struct {
	DocumentID string
	Revision   int
	Operation  string
	Title      string
	SourceURL  string
	SHA256     string
	BlobKey    string
	SizeBytes  int64
	MimeType   string
	CreatedAt  time.Time
}

// ListQuery filters owner-visible documents. Cursor is an opaque page token.
type ListQuery struct {
	OwnerID string
	Query   string // filename substring, case-insensitive
	Limit   int
	Cursor  string
}

type ListResult struct {
	Documents  []Document
	NextCursor string
}

// Repository is implemented by PostgreSQL (T10). Every method is scoped by
// owner — never fetch-then-check in handlers (plan T10).
type Repository interface {
	Create(ctx context.Context, doc Document, v Version) error
	List(ctx context.Context, q ListQuery) (ListResult, error)
	// Get returns ErrNotFound for missing, soft-deleted, or foreign documents.
	Get(ctx context.Context, ownerID, id string) (Document, error)
	// VersionAt returns the head content version (latest revision carrying a
	// blob) for downloads, or any explicit revision when rev > 0.
	VersionAt(ctx context.Context, ownerID, id string, rev int) (Version, error)
	// Update applies a CAS transition: the new revision lands in the same
	// transaction as the current_revision bump, and ErrRevisionConflict is
	// returned when expectedRev no longer matches (plan T17).
	Update(ctx context.Context, ownerID, id string, expectedRev int, deleted bool, v Version) error
}

// BlobStore is the R2 contract this service needs (port.RemoteContentStore
// satisfies it).
type BlobStore interface {
	Put(ctx context.Context, key string, body io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

type IDGenerator func() (string, error)

// Service owns document rules. It is deliberately free of HTTP concepts.
type Service struct {
	repo         Repository
	blobs        BlobStore
	ids          IDGenerator
	maxUpload    int64
	sniffBufSize int64
}

func NewService(repo Repository, blobs BlobStore, ids IDGenerator, maxUpload int64) *Service {
	return &Service{repo: repo, blobs: blobs, ids: ids, maxUpload: maxUpload, sniffBufSize: 4096}
}

// DetectType validates magic bytes / textiness. Stage 1 allows PDF and
// Markdown only (plan T15).
func DetectType(name string, head []byte) (mime string, err error) {
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return "application/pdf", nil
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") {
		if isText(head) {
			return "text/markdown", nil
		}
	}
	return "", ErrUnsupportedType
}

func isText(head []byte) bool {
	for _, b := range head {
		if b == 0 {
			return false
		}
	}
	return true
}

// BlobKey is the content-addressed object key, matching local blobstore
// semantics (ADR-002).
func BlobKey(sha256Hex string) string { return "sha256/" + sha256Hex }

// Create ingests one upload: spool to a temp file while hashing → validate
// magic → R2 → single PG transaction (document + v1). R2 failure means no
// metadata is published (plan T19); a PG failure may orphan the blob, which
// GC may reclaim later — we never delete inline because identical content
// may be referenced elsewhere. Uploads are spooled to disk, never buffered
// whole in memory (plan T15).
func (s *Service) Create(ctx context.Context, ownerID, filename, sourceURL string, body io.Reader) (Document, error) {
	spool, err := s.spool(body)
	if err != nil {
		return Document{}, err
	}
	defer spool.cleanup()
	mime, err := DetectType(filename, spool.head)
	if err != nil {
		return Document{}, err
	}
	if err := spool.rewind(); err != nil {
		return Document{}, err
	}
	key := BlobKey(spool.sha256)
	if err := s.blobs.Put(ctx, key, spool.file, spool.size); err != nil {
		return Document{}, err
	}

	id, err := s.ids()
	if err != nil {
		return Document{}, err
	}
	title := strings.TrimSpace(filename)
	now := time.Now().UTC()
	doc := Document{
		ID: id, OwnerID: ownerID, CurrentRevision: 1,
		Title: title, SourceURL: strings.TrimSpace(sourceURL),
		CreatedAt: now, UpdatedAt: now,
	}
	v := Version{
		DocumentID: id, Revision: 1, Operation: OpCreate,
		Title: title, SourceURL: doc.SourceURL,
		SHA256: spool.sha256, BlobKey: key, SizeBytes: spool.size, MimeType: mime,
		CreatedAt: now,
	}
	if err := s.repo.Create(ctx, doc, v); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (s *Service) List(ctx context.Context, q ListQuery) (ListResult, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	return s.repo.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Document, error) {
	return s.repo.Get(ctx, ownerID, id)
}

// OpenContent resolves the content version and streams the blob.
func (s *Service) OpenContent(ctx context.Context, ownerID, id string, rev int) (Version, io.ReadCloser, error) {
	version, err := s.repo.VersionAt(ctx, ownerID, id, rev)
	if err != nil {
		return Version{}, nil, err
	}
	if version.BlobKey == "" {
		return Version{}, nil, ErrNotFound
	}
	body, err := s.blobs.Get(ctx, version.BlobKey)
	if err != nil {
		return Version{}, nil, err
	}
	return version, body, nil
}

// UpdateMeta applies a title/source_url change under If-Match (T17).
func (s *Service) UpdateMeta(ctx context.Context, ownerID, id string, expectedRev int, title, sourceURL string) (Document, error) {
	doc, err := s.repo.Get(ctx, ownerID, id)
	if err != nil {
		return Document{}, err
	}
	if strings.TrimSpace(title) != "" {
		doc.Title = strings.TrimSpace(title)
	}
	doc.SourceURL = strings.TrimSpace(sourceURL)
	v := Version{
		DocumentID: id, Revision: expectedRev + 1, Operation: OpUpdateMeta,
		Title: doc.Title, SourceURL: doc.SourceURL, CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Update(ctx, ownerID, id, expectedRev, false, v); err != nil {
		return Document{}, err
	}
	doc.CurrentRevision = expectedRev + 1
	return doc, nil
}

// ReplaceContent uploads a new body and records a new revision under If-Match.
// Old blobs are retained (plan T17).
func (s *Service) ReplaceContent(ctx context.Context, ownerID, id string, expectedRev int, filename string, body io.Reader) (Document, error) {
	doc, err := s.repo.Get(ctx, ownerID, id)
	if err != nil {
		return Document{}, err
	}
	spool, err := s.spool(body)
	if err != nil {
		return Document{}, err
	}
	defer spool.cleanup()
	mime, err := DetectType(filename, spool.head)
	if err != nil {
		return Document{}, err
	}
	if err := spool.rewind(); err != nil {
		return Document{}, err
	}
	if err := s.blobs.Put(ctx, BlobKey(spool.sha256), spool.file, spool.size); err != nil {
		return Document{}, err
	}
	v := Version{
		DocumentID: id, Revision: expectedRev + 1, Operation: OpUpdateContent,
		Title: doc.Title, SourceURL: doc.SourceURL,
		SHA256: spool.sha256, BlobKey: BlobKey(spool.sha256), SizeBytes: spool.size, MimeType: mime,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Update(ctx, ownerID, id, expectedRev, false, v); err != nil {
		return Document{}, err
	}
	doc.CurrentRevision = expectedRev + 1
	return doc, nil
}

// SoftDelete writes a tombstone revision and sets deleted_at; R2 content is
// retained (plan T18).
func (s *Service) SoftDelete(ctx context.Context, ownerID, id string, expectedRev int) error {
	doc, err := s.repo.Get(ctx, ownerID, id)
	if err != nil {
		return err
	}
	v := Version{
		DocumentID: id, Revision: expectedRev + 1, Operation: OpDelete,
		Title: doc.Title, SourceURL: doc.SourceURL, CreatedAt: time.Now().UTC(),
	}
	return s.repo.Update(ctx, ownerID, id, expectedRev, true, v)
}

// spooledUpload holds a bounded upload staged on disk: SHA-256 and the sniff
// buffer were computed during the single inbound pass.
type spooledUpload struct {
	file   *os.File
	head   []byte
	sha256 string
	size   int64
}

func (s *Service) spool(body io.Reader) (*spooledUpload, error) {
	tmp, err := os.CreateTemp("", "membox-upload-*")
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	var sniff bytes.Buffer
	writer := io.MultiWriter(tmp, hash)
	limited := io.LimitReader(body, s.maxUpload+1)
	size, err := io.Copy(writer, io.TeeReader(io.LimitReader(limited, s.sniffBufSize), &sniff))
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	// The tee only covered the first sniffBufSize bytes; copy the remainder.
	rest, err := io.Copy(writer, limited)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	size += rest
	if size > s.maxUpload {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("upload exceeds %d bytes", s.maxUpload)
	}
	return &spooledUpload{
		file:   tmp,
		head:   sniff.Bytes(),
		sha256: hex.EncodeToString(hash.Sum(nil)),
		size:   size,
	}, nil
}

func (s *spooledUpload) rewind() error {
	_, err := s.file.Seek(0, io.SeekStart)
	return err
}

func (s *spooledUpload) cleanup() {
	_ = s.file.Close()
	_ = os.Remove(s.file.Name())
}
