// Package sqlite implements repository.NoteMetaRepository on top of SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/earendil-works/membox/internal/domain"
	"github.com/earendil-works/membox/internal/repository"
)

// NoteMetaRepo is the SQLite-backed implementation of repository.NoteMetaRepository.
type NoteMetaRepo struct {
	db *sql.DB
}

// NewNoteMetaRepo creates a metadata repository backed by the provided database.
func NewNoteMetaRepo(db *sql.DB) *NoteMetaRepo {
	return &NoteMetaRepo{db: db}
}

// CreateMeta persists note metadata.
func (r *NoteMetaRepo) CreateMeta(ctx context.Context, n *domain.Note) error {
	tagsJSON, err := json.Marshal(n.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO note (uuid, title, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, n.UUID, n.Title, string(tagsJSON), n.CreatedAt.Format(time.RFC3339), n.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert note meta: %w", err)
	}
	return nil
}

// GetMetaByUUID loads metadata for a single note by UUID.
func (r *NoteMetaRepo) GetMetaByUUID(ctx context.Context, uuid string) (*domain.Note, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT uuid, title, tags, created_at, updated_at
		FROM note
		WHERE uuid = ?
	`, uuid)

	n, err := scanNote(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return n, nil
}

// UpdateMeta replaces mutable metadata fields.
func (r *NoteMetaRepo) UpdateMeta(ctx context.Context, n *domain.Note) error {
	tagsJSON, err := json.Marshal(n.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE note
		SET title = ?, tags = ?, updated_at = ?
		WHERE uuid = ?
	`, n.Title, string(tagsJSON), n.UpdatedAt.Format(time.RFC3339), n.UUID)
	if err != nil {
		return fmt.Errorf("update note meta: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// DeleteMeta removes metadata for a note by UUID.
func (r *NoteMetaRepo) DeleteMeta(ctx context.Context, uuid string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM note WHERE uuid = ?`, uuid)
	if err != nil {
		return fmt.Errorf("delete note meta: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// ListMeta returns recent note metadata, optionally filtered by included/excluded tags.
func (r *NoteMetaRepo) ListMeta(ctx context.Context, includeTags, excludeTags []string, limit int) ([]*domain.Note, error) {
	if limit <= 0 {
		limit = 20
	}

	where, params := []string{}, []any{}
	for _, t := range includeTags {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)")
		params = append(params, t)
	}
	for _, t := range excludeTags {
		where = append(where, "NOT EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)")
		params = append(params, t)
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT uuid, title, tags, created_at, updated_at
		FROM note
		%s
		ORDER BY updated_at DESC
		LIMIT ?
	`, whereSQL), append(params, limit)...)
	if err != nil {
		return nil, fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	var out []*domain.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// FindByUUIDPrefix returns the unique note whose UUID starts with prefix.
func (r *NoteMetaRepo) FindByUUIDPrefix(ctx context.Context, prefix string) (*domain.Note, error) {
	if prefix == "" {
		return nil, fmt.Errorf("uuid prefix cannot be empty")
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT uuid, title, tags, created_at, updated_at
		FROM note
		WHERE uuid LIKE ?
	`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("query uuid prefix: %w", err)
	}
	defer rows.Close()

	var matches []*domain.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	switch len(matches) {
	case 0:
		return nil, repository.ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("uuid prefix %q matches multiple notes", prefix)
	}
}

// SearchMeta performs FTS5 search over note titles.
func (r *NoteMetaRepo) SearchMeta(ctx context.Context, query string, limit int) ([]*repository.NoteSearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT n.uuid, n.title, n.tags,
		       n.created_at, n.updated_at,
		       snippet(note_fts, 0, '[', ']', '…', 24) AS snippet
		FROM note_fts
		JOIN note n ON n.rowid = note_fts.rowid
		WHERE note_fts MATCH ?
		ORDER BY bm25(note_fts)
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	defer rows.Close()

	var out []*repository.NoteSearchResult
	for rows.Next() {
		n, snippet, err := scanNoteWithSnippet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &repository.NoteSearchResult{Note: n, Snippet: snippet})
	}
	return out, rows.Err()
}

// --- helpers ---

type scanner interface {
	Scan(dest ...any) error
}

func scanNote(s scanner) (*domain.Note, error) {
	var n domain.Note
	var tagsJSON, createdAt, updatedAt string
	if err := s.Scan(
		&n.UUID, &n.Title,
		&tagsJSON, &createdAt, &updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan note: %w", err)
	}
	_ = json.Unmarshal([]byte(tagsJSON), &n.Tags)
	n.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &n, nil
}

func scanNoteWithSnippet(s scanner) (*domain.Note, string, error) {
	var n domain.Note
	var tagsJSON, createdAt, updatedAt, snippet string
	if err := s.Scan(
		&n.UUID, &n.Title,
		&tagsJSON, &createdAt, &updatedAt, &snippet,
	); err != nil {
		return nil, "", fmt.Errorf("scan note: %w", err)
	}
	_ = json.Unmarshal([]byte(tagsJSON), &n.Tags)
	n.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &n, snippet, nil
}
