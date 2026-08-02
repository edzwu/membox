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

type IndexState struct {
	Title     string
	Summary   string
	MTime     int64
	Size      int64
	SHA256    string
	IndexedAt time.Time
}

type Observation struct {
	Location Location
	FileKey  FileKey
	Title    string
	MTime    int64
	Size     int64
	SHA256   string
	Body     []byte
}

type Document struct {
	ID        DocumentID
	Location  Location
	FileKey   FileKey
	Status    DocumentStatus
	Index     IndexState
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewDocument(id DocumentID, observation Observation, now time.Time) (*Document, error) {
	if strings.TrimSpace(string(id)) == "" {
		return nil, errors.New("document ID is required")
	}
	if err := validateObservation(observation); err != nil {
		return nil, err
	}
	return &Document{
		ID:       id,
		Location: observation.Location,
		FileKey:  observation.FileKey,
		Status:   DocumentActive,
		Index: IndexState{
			Title: observation.Title, MTime: observation.MTime, Size: observation.Size,
			SHA256: observation.SHA256, IndexedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func RehydrateDocument(id DocumentID, location Location, fileKey FileKey, status DocumentStatus, index IndexState, createdAt, updatedAt time.Time) (*Document, error) {
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
	return &Document{ID: id, Location: location, FileKey: fileKey, Status: status, Index: index, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
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

func (d *Document) applyObservation(observation Observation, now time.Time) {
	d.FileKey = observation.FileKey
	d.Status = DocumentActive
	d.Index = IndexState{
		Title: observation.Title, Summary: d.Index.Summary, MTime: observation.MTime, Size: observation.Size,
		SHA256: observation.SHA256, IndexedAt: now,
	}
	d.UpdatedAt = now
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
