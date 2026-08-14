package catalog

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

type DocumentID string
type IndexedPathID int64

type DocumentStatus string

// TrashDir is the per-path-root directory holding soft-deleted documents.
// Scanners skip it and queries hide its members; documents keep their UUID,
// relations, and index entries until purged, so a restore is a file move.
const TrashDir = ".membox-trash"

// IsTrashedPath reports whether a relative location path points inside the
// trash directory of its path root.
func IsTrashedPath(relativePath string) bool {
	return strings.HasPrefix(strings.ReplaceAll(relativePath, "\\", "/"), TrashDir+"/")
}

const (
	DocumentActive    DocumentStatus = "active"
	DocumentMissing   DocumentStatus = "missing"
	DocumentUntracked DocumentStatus = "untracked"
)

type PathStatus string

const (
	PathReady   PathStatus = "ready"
	PathPartial PathStatus = "partial"
	PathRemoved PathStatus = "removed"
)

type Location struct {
	PathID       IndexedPathID
	RelativePath string
}

func NewLocation(pathID IndexedPathID, relative string) (Location, error) {
	relative = strings.TrimSpace(strings.ReplaceAll(relative, "\\", "/"))
	clean := path.Clean(relative)
	if pathID <= 0 {
		return Location{}, errors.New("path ID must be positive")
	}
	if relative == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return Location{}, fmt.Errorf("invalid relative path %q", relative)
	}
	return Location{PathID: pathID, RelativePath: clean}, nil
}

type ContentFingerprint struct {
	SHA256 string
	Size   int64
}

type FileKey string

const (
	MetadataTitleOverride = 1 << iota
	MetadataAuthorsOverride
	MetadataYearOverride
	MetadataKeywordsOverride
)

type IndexState struct {
	Title             string
	Summary           string
	MediaType         string
	MetadataOverrides int
	Authors           string
	Year              int
	Keywords          string
	PageCount         int
	MTime             int64
	Size              int64
	SHA256            string
	IndexedAt         time.Time
	SourceCreatedAt   time.Time
	SourceUpdatedAt   time.Time
}

type Observation struct {
	Location   Location
	FileKey    FileKey
	Title      string
	MediaType  string
	Authors    string
	Year       int
	Keywords   string
	PageCount  int
	MTime      int64
	Size       int64
	SHA256     string
	Body       []byte // authoritative file bytes
	SearchText []byte // rebuildable text projection; Body for Markdown
}

type Document struct {
	ID        DocumentID
	Location  Location
	FileKey   FileKey
	Status    DocumentStatus
	Index     IndexState
	CreatedAt time.Time
	UpdatedAt time.Time
	Pinned    bool
}

func NewDocument(id DocumentID, observation Observation, now time.Time) (*Document, error) {
	if strings.TrimSpace(string(id)) == "" {
		return nil, errors.New("document ID is required")
	}
	if err := validateObservation(observation); err != nil {
		return nil, err
	}
	fileTime := time.Unix(0, observation.MTime)
	return &Document{
		ID:       id,
		Location: observation.Location,
		FileKey:  observation.FileKey,
		Status:   DocumentActive,
		Index: IndexState{
			Title: observation.Title, MediaType: normalizedMediaType(observation.MediaType), Authors: observation.Authors,
			Year: observation.Year, Keywords: observation.Keywords, PageCount: observation.PageCount,
			MTime: observation.MTime, Size: observation.Size, SHA256: observation.SHA256, IndexedAt: now,
			SourceCreatedAt: fileTime, SourceUpdatedAt: fileTime,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func RehydrateDocument(id DocumentID, location Location, fileKey FileKey, status DocumentStatus, index IndexState, createdAt, updatedAt time.Time, pinned bool) (*Document, error) {
	if strings.TrimSpace(string(id)) == "" {
		return nil, errors.New("document ID is required")
	}
	if location.PathID <= 0 || location.RelativePath == "" {
		return nil, errors.New("document location is invalid")
	}
	switch status {
	case DocumentActive, DocumentMissing, DocumentUntracked:
	default:
		return nil, fmt.Errorf("invalid document status %q", status)
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		return nil, errors.New("document timestamps are required")
	}
	index.MediaType = normalizedMediaType(index.MediaType)
	return &Document{ID: id, Location: location, FileKey: fileKey, Status: status, Index: index, CreatedAt: createdAt, UpdatedAt: updatedAt, Pinned: pinned}, nil
}

func (d *Document) Observe(observation Observation, now time.Time) error {
	if observation.Location != d.Location {
		return errors.New("observation location does not match document location")
	}
	if err := validateObservation(observation); err != nil {
		return err
	}
	d.applyObservation(observation, now)
	return nil
}

func (d *Document) Relocate(location Location, observation Observation, now time.Time) error {
	if observation.Location != location {
		return errors.New("relocation and observation locations do not match")
	}
	if err := validateObservation(observation); err != nil {
		return err
	}
	d.Location = location
	d.applyObservation(observation, now)
	// A path/name change is a user-visible modification even when the Markdown
	// bytes and filesystem mtime are unchanged.
	d.Index.SourceUpdatedAt = now
	return nil
}

func (d *Document) MarkMissing(now time.Time) {
	d.Status = DocumentMissing
	d.UpdatedAt = now
}

func (d *Document) MarkUntracked(now time.Time) {
	d.Status = DocumentUntracked
	d.UpdatedAt = now
}

// SetSourceTimes replaces the document dates shown to users with dates from
// the content's source history (for example, Git). Aggregate audit timestamps
// remain separate from these source timestamps.
func (d *Document) SetPinned(pinned bool) { d.Pinned = pinned }

func (d *Document) SetSourceTimes(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() || updatedAt.IsZero() {
		return errors.New("source timestamps are required")
	}
	d.Index.SourceCreatedAt = createdAt
	d.Index.SourceUpdatedAt = updatedAt
	return nil
}

func (d *Document) applyObservation(observation Observation, now time.Time) {
	fileTime := time.Unix(0, observation.MTime)
	sourceCreatedAt := d.Index.SourceCreatedAt
	sourceUpdatedAt := d.Index.SourceUpdatedAt
	if sourceCreatedAt.IsZero() {
		sourceCreatedAt = fileTime
	}
	// Preserve historical dates across no-op scans. Relocate explicitly raises
	// the modified time because a path/name change is user-visible. If content
	// changes before it is committed, filesystem mtime is the best fallback
	// until the user syncs Git history again.
	if sourceUpdatedAt.IsZero() || d.Index.SHA256 != observation.SHA256 {
		sourceUpdatedAt = fileTime
	}
	d.FileKey = observation.FileKey
	d.Status = DocumentActive
	mediaType := normalizedMediaType(observation.MediaType)
	title, authors, year, keywords := observation.Title, observation.Authors, observation.Year, observation.Keywords
	// PDF metadata can be edited in membox without rewriting the binary. Keep
	// those catalog values across scans; page count remains extractor-owned.
	if mediaType == "application/pdf" && d.Index.MediaType == mediaType {
		if d.Index.MetadataOverrides&MetadataTitleOverride != 0 {
			title = d.Index.Title
		}
		if d.Index.MetadataOverrides&MetadataAuthorsOverride != 0 {
			authors = d.Index.Authors
		}
		if d.Index.MetadataOverrides&MetadataYearOverride != 0 {
			year = d.Index.Year
		}
		if d.Index.MetadataOverrides&MetadataKeywordsOverride != 0 {
			keywords = d.Index.Keywords
		}
	}
	d.Index = IndexState{
		Title: title, Summary: d.Index.Summary, MediaType: mediaType, MetadataOverrides: d.Index.MetadataOverrides,
		Authors: authors, Year: year, Keywords: keywords, PageCount: observation.PageCount, MTime: observation.MTime, Size: observation.Size,
		SHA256: observation.SHA256, IndexedAt: now, SourceCreatedAt: sourceCreatedAt, SourceUpdatedAt: sourceUpdatedAt,
	}
	d.UpdatedAt = now
}

func normalizedMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "text/markdown"
	}
	return value
}

func validateObservation(observation Observation) error {
	if observation.Location.PathID <= 0 || observation.Location.RelativePath == "" {
		return errors.New("observation location is required")
	}
	if observation.Size < 0 {
		return errors.New("observation size cannot be negative")
	}
	if len(observation.SHA256) != 64 {
		return errors.New("observation SHA-256 is invalid")
	}
	return nil
}

type IndexedPath struct {
	ID         IndexedPathID
	Root       string
	Status     PathStatus
	CreatedAt  time.Time
	LastScanAt *time.Time
	LastError  string
}

func RehydrateIndexedPath(id IndexedPathID, root string, status PathStatus, createdAt time.Time, lastScanAt *time.Time, lastError string) (*IndexedPath, error) {
	if id <= 0 || strings.TrimSpace(root) == "" || createdAt.IsZero() {
		return nil, errors.New("invalid indexed path")
	}
	switch status {
	case PathReady, PathPartial, PathRemoved:
	default:
		return nil, fmt.Errorf("invalid path status %q", status)
	}
	return &IndexedPath{ID: id, Root: root, Status: status, CreatedAt: createdAt, LastScanAt: lastScanAt, LastError: lastError}, nil
}

func (p *IndexedPath) RecordScan(now time.Time, scanErr error) {
	p.LastScanAt = &now
	if scanErr != nil {
		p.Status = PathPartial
		p.LastError = scanErr.Error()
		return
	}
	p.Status = PathReady
	p.LastError = ""
}

func (p *IndexedPath) Remove() { p.Status = PathRemoved }
func (p *IndexedPath) Activate() {
	p.Status = PathReady
	p.LastError = ""
}
