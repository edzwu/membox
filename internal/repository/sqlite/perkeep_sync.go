// Package sqlite implements persistence for Perkeep sync state.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PerkeepSyncRepo persists the mapping between local note UUIDs and Perkeep
// permanode blobrefs, plus the content hash at the time of last sync.
type PerkeepSyncRepo struct {
	db *sql.DB
}

// NewPerkeepSyncRepo creates a repository backed by the provided database.
func NewPerkeepSyncRepo(db *sql.DB) *PerkeepSyncRepo {
	return &PerkeepSyncRepo{db: db}
}

// SyncRecord holds the state for a single synced note.
type SyncRecord struct {
	UUID         string
	Permanode    string
	ContentHash  string
	SyncedAt     string
}

// GetByUUID returns the sync record for a note, or sql.ErrNoRows if missing.
func (r *PerkeepSyncRepo) GetByUUID(ctx context.Context, uuid string) (*SyncRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT uuid, permanode, content_hash, synced_at
		FROM perkeep_sync
		WHERE uuid = ?
	`, uuid)

	var rec SyncRecord
	if err := row.Scan(&rec.UUID, &rec.Permanode, &rec.ContentHash, &rec.SyncedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get perkeep sync record: %w", err)
	}
	return &rec, nil
}

// Save upserts the sync record for a note.
func (r *PerkeepSyncRepo) Save(ctx context.Context, rec *SyncRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO perkeep_sync (uuid, permanode, content_hash, synced_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(uuid) DO UPDATE SET
			permanode = excluded.permanode,
			content_hash = excluded.content_hash,
			synced_at = excluded.synced_at
	`, rec.UUID, rec.Permanode, rec.ContentHash)
	if err != nil {
		return fmt.Errorf("save perkeep sync record: %w", err)
	}
	return nil
}

// Delete removes the sync record for a note.
func (r *PerkeepSyncRepo) Delete(ctx context.Context, uuid string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM perkeep_sync WHERE uuid = ?`, uuid)
	if err != nil {
		return fmt.Errorf("delete perkeep sync record: %w", err)
	}
	return nil
}

// List returns all known sync records.
func (r *PerkeepSyncRepo) List(ctx context.Context) ([]*SyncRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT uuid, permanode, content_hash, synced_at
		FROM perkeep_sync
		ORDER BY synced_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list perkeep sync records: %w", err)
	}
	defer rows.Close()

	var out []*SyncRecord
	for rows.Next() {
		var rec SyncRecord
		if err := rows.Scan(&rec.UUID, &rec.Permanode, &rec.ContentHash, &rec.SyncedAt); err != nil {
			return nil, fmt.Errorf("scan perkeep sync record: %w", err)
		}
		out = append(out, &rec)
	}
	return out, rows.Err()
}
