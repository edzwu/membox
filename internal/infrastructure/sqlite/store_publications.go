package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

// UpsertPublication records or refreshes the publication state for a document.
func (s *Store) UpsertPublication(ctx context.Context, record port.PublicationRecord) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO publications(document_id,slug,lang,published_sha256,published_at)
VALUES(?,?,?,?,?)
ON CONFLICT(document_id) DO UPDATE SET
    slug=excluded.slug,
    lang=excluded.lang,
    published_sha256=excluded.published_sha256,
    published_at=excluded.published_at`,
		record.DocumentID, record.Slug, record.Lang, record.PublishedSHA256, millis(record.PublishedAt)); err != nil {
		return fmt.Errorf("upserting publication for %s: %w", record.DocumentID, err)
	}
	return nil
}

// GetPublication returns the publication record for a document, if any.
func (s *Store) GetPublication(ctx context.Context, documentID catalog.DocumentID) (port.PublicationRecord, bool, error) {
	var record port.PublicationRecord
	var publishedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT document_id,slug,lang,published_sha256,published_at
FROM publications WHERE document_id=?`, string(documentID)).
		Scan(&record.DocumentID, &record.Slug, &record.Lang, &record.PublishedSHA256, &publishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.PublicationRecord{}, false, nil
	}
	if err != nil {
		return port.PublicationRecord{}, false, fmt.Errorf("reading publication for %s: %w", documentID, err)
	}
	record.PublishedAt = fromMillis(publishedAt)
	return record, true, nil
}

// DeletePublication removes the publication record for a document.
func (s *Store) DeletePublication(ctx context.Context, documentID catalog.DocumentID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM publications WHERE document_id=?`, string(documentID)); err != nil {
		return fmt.Errorf("deleting publication for %s: %w", documentID, err)
	}
	return nil
}

// ListPublications returns every publication record, newest first.
func (s *Store) ListPublications(ctx context.Context) ([]port.PublicationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document_id,slug,lang,published_sha256,published_at
FROM publications ORDER BY published_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing publications: %w", err)
	}
	defer rows.Close()
	var records []port.PublicationRecord
	for rows.Next() {
		var record port.PublicationRecord
		var publishedAt int64
		if err := rows.Scan(&record.DocumentID, &record.Slug, &record.Lang, &record.PublishedSHA256, &publishedAt); err != nil {
			return nil, fmt.Errorf("scanning publication: %w", err)
		}
		record.PublishedAt = fromMillis(publishedAt)
		records = append(records, record)
	}
	return records, rows.Err()
}
