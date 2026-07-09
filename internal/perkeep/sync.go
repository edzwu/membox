// Package perkeep provides sync logic between local membox notes and a Perkeep server.
package perkeep

import (
	"context"
	"fmt"
	"os"

	"github.com/earendil-works/membox/internal/domain"
	"github.com/earendil-works/membox/internal/repository"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
)

// Syncer pushes local notes to Perkeep and records the resulting permanode
// blobrefs so the notes can be retrieved later.
type Syncer struct {
	client    *Client
	store     repository.NoteStore
	repo      repository.NoteMetaRepository
	syncRepo  *sqliterepo.PerkeepSyncRepo
	cacheRoot string
}

// NewSyncer creates a syncer for the given store and repositories.
func NewSyncer(client *Client, store repository.NoteStore, repo repository.NoteMetaRepository, syncRepo *sqliterepo.PerkeepSyncRepo, cacheRoot string) *Syncer {
	return &Syncer{
		client:    client,
		store:     store,
		repo:      repo,
		syncRepo:  syncRepo,
		cacheRoot: cacheRoot,
	}
}

// SyncResult reports how many notes were uploaded, skipped, or failed.
type SyncResult struct {
	Uploaded int
	Skipped  int
	Failed   int
	Errors   []error
}

// Push uploads all notes that have changed since the last sync.
func (s *Syncer) Push(ctx context.Context) (*SyncResult, error) {
	notes, err := s.store.Scan()
	if err != nil {
		return nil, fmt.Errorf("scan notes: %w", err)
	}

	res := &SyncResult{}
	for _, n := range notes {
		if err := s.pushNote(ctx, n, res); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Errorf("uuid %s: %w", n.UUID, err))
		}
	}
	return res, nil
}

func (s *Syncer) pushNote(ctx context.Context, n *domain.Note, res *SyncResult) error {
	// Load full content if it wasn't included by Scan.
	full, err := s.store.Load(n.UUID)
	if err != nil {
		return fmt.Errorf("load note: %w", err)
	}
	// Reconcile metadata from the authoritative repo.
	meta, err := s.repo.GetMetaByUUID(ctx, n.UUID)
	if err == nil {
		full.Title = meta.Title
		full.Tags = meta.Tags
	}

	data := ComposeMarkdown(full.UUID, full.Content)
	hash := HashContent(data)

	rec, err := s.syncRepo.GetByUUID(ctx, full.UUID)
	if err == nil && rec.ContentHash == hash {
		res.Skipped++
		return nil
	}

	tmpPath, err := WriteTempNote(s.cacheRoot, data)
	if err != nil {
		return fmt.Errorf("stage note: %w", err)
	}
	defer os.Remove(tmpPath)

	up, err := s.client.UploadNote(ctx, tmpPath, full.Title, full.Tags, full.UUID)
	if err != nil {
		return fmt.Errorf("upload to perkeep: %w", err)
	}

	if err := s.syncRepo.Save(ctx, &sqliterepo.SyncRecord{
		UUID:        full.UUID,
		Permanode:   up.Permanode,
		ContentHash: hash,
	}); err != nil {
		return fmt.Errorf("record sync state: %w", err)
	}

	res.Uploaded++
	return nil
}

// ListSynced returns the UUIDs of all notes that have been pushed to Perkeep.
func (s *Syncer) ListSynced(ctx context.Context) ([]*sqliterepo.SyncRecord, error) {
	return s.syncRepo.List(ctx)
}
