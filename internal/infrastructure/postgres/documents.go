package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"membox/internal/remotedocs"
)

// DocumentStore implements remotedocs.Repository. Every query filters by
// owner_id (plan T10: ownership is a WHERE clause, never a handler afterthought).
type DocumentStore struct {
	pool *pgxpool.Pool
}

func NewDocumentStore(pool *pgxpool.Pool) *DocumentStore { return &DocumentStore{pool: pool} }

func (s *DocumentStore) Create(ctx context.Context, doc remotedocs.Document, v remotedocs.Version) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO documents(id, owner_id, current_revision, title, source_url, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		doc.ID, doc.OwnerID, doc.CurrentRevision, doc.Title, doc.SourceURL, doc.CreatedAt, doc.UpdatedAt); err != nil {
		return fmt.Errorf("insert document: %w", err)
	}
	if err := insertVersion(ctx, tx, v); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// listCursor is the opaque page token: updated_at + id of the last row.
type listCursor struct {
	UpdatedAt time.Time
	ID        string
}

func encodeCursor(c listCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%d|%s", c.UpdatedAt.UnixNano(), c.ID)))
}

func decodeCursor(token string) (listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return listCursor{}, remotedocs.ErrNotFound
	}
	nanos, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return listCursor{}, remotedocs.ErrNotFound
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return listCursor{}, remotedocs.ErrNotFound
	}
	return listCursor{UpdatedAt: time.Unix(0, n), ID: id}, nil
}

func (s *DocumentStore) List(ctx context.Context, q remotedocs.ListQuery) (remotedocs.ListResult, error) {
	where := "d.owner_id = $1 AND d.deleted_at IS NULL"
	args := []any{q.OwnerID}
	if q.Query != "" {
		args = append(args, "%"+strings.ToLower(q.Query)+"%")
		where += fmt.Sprintf(" AND lower(d.title) LIKE $%d", len(args))
	}
	if q.Cursor != "" {
		cursor, err := decodeCursor(q.Cursor)
		if err != nil {
			return remotedocs.ListResult{}, err
		}
		args = append(args, cursor.UpdatedAt, cursor.ID)
		where += fmt.Sprintf(" AND (d.updated_at, d.id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, q.Limit+1)
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.current_revision, d.title, d.source_url, d.created_at, d.updated_at,
		       COALESCE(v.size_bytes, 0), COALESCE(v.mime_type, '')
		FROM documents d
		LEFT JOIN LATERAL (
		       SELECT size_bytes, mime_type
		       FROM document_versions
		       WHERE document_id = d.id AND blob_key <> ''
		       ORDER BY revision DESC LIMIT 1
		) v ON true
		WHERE `+where+`
		ORDER BY d.updated_at DESC, d.id DESC
		LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return remotedocs.ListResult{}, err
	}
	defer rows.Close()

	result := remotedocs.ListResult{}
	for rows.Next() {
		var doc remotedocs.Document
		if err := rows.Scan(&doc.ID, &doc.CurrentRevision, &doc.Title, &doc.SourceURL, &doc.CreatedAt, &doc.UpdatedAt, &doc.SizeBytes, &doc.MimeType); err != nil {
			return remotedocs.ListResult{}, err
		}
		doc.OwnerID = q.OwnerID
		result.Documents = append(result.Documents, doc)
	}
	if err := rows.Err(); err != nil {
		return remotedocs.ListResult{}, err
	}
	if len(result.Documents) > q.Limit {
		result.Documents = result.Documents[:q.Limit]
		last := result.Documents[len(result.Documents)-1]
		result.NextCursor = encodeCursor(listCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
	}
	return result, nil
}

func (s *DocumentStore) Get(ctx context.Context, ownerID, id string) (remotedocs.Document, error) {
	var doc remotedocs.Document
	err := s.pool.QueryRow(ctx, `
		SELECT d.id, d.current_revision, d.title, d.source_url, d.created_at, d.updated_at,
		       COALESCE(v.size_bytes, 0), COALESCE(v.mime_type, '')
		FROM documents d
		LEFT JOIN LATERAL (
		       SELECT size_bytes, mime_type
		       FROM document_versions
		       WHERE document_id = d.id AND blob_key <> ''
		       ORDER BY revision DESC LIMIT 1
		) v ON true
		WHERE d.owner_id = $1 AND d.id = $2 AND d.deleted_at IS NULL`,
		ownerID, id,
	).Scan(&doc.ID, &doc.CurrentRevision, &doc.Title, &doc.SourceURL, &doc.CreatedAt, &doc.UpdatedAt, &doc.SizeBytes, &doc.MimeType)
	if errors.Is(err, pgx.ErrNoRows) {
		return remotedocs.Document{}, remotedocs.ErrNotFound
	}
	if err != nil {
		return remotedocs.Document{}, err
	}
	doc.OwnerID = ownerID
	return doc, nil
}

// VersionAt returns an explicit revision (rev > 0), or the latest
// blob-carrying revision for downloads. Tombstones carry no blob.
func (s *DocumentStore) VersionAt(ctx context.Context, ownerID, id string, rev int) (remotedocs.Version, error) {
	if _, err := s.Get(ctx, ownerID, id); err != nil {
		return remotedocs.Version{}, err
	}
	var v remotedocs.Version
	var err error
	if rev > 0 {
		err = s.pool.QueryRow(ctx, versionSelect+` AND v.revision = $2`, id, rev).Scan(versionScanTargets(&v)...)
	} else {
		err = s.pool.QueryRow(ctx, versionSelect+` AND v.blob_key <> '' ORDER BY v.revision DESC LIMIT 1`, id).Scan(versionScanTargets(&v)...)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return remotedocs.Version{}, remotedocs.ErrNotFound
	}
	return v, err
}

const versionSelect = `
	SELECT v.document_id, v.revision, v.operation, v.title, v.source_url,
	       v.sha256, v.blob_key, v.size_bytes, v.mime_type, v.created_at
	FROM document_versions v
	WHERE v.document_id = $1`

func versionScanTargets(v *remotedocs.Version) []any {
	return []any{&v.DocumentID, &v.Revision, &v.Operation, &v.Title, &v.SourceURL,
		&v.SHA256, &v.BlobKey, &v.SizeBytes, &v.MimeType, &v.CreatedAt}
}

// Update is the CAS transition: bump current_revision and insert the new
// immutable version in one transaction. Mismatch → ErrRevisionConflict (412).
func (s *DocumentStore) Update(ctx context.Context, ownerID, id string, expectedRev int, deleted bool, v remotedocs.Version) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE documents
		SET current_revision = $3, title = $4, source_url = $5,
		    updated_at = $6, deleted_at = CASE WHEN $7 THEN $6 ELSE deleted_at END
		WHERE owner_id = $1 AND id = $2 AND current_revision = $8 AND deleted_at IS NULL`,
		ownerID, id, expectedRev+1, v.Title, v.SourceURL, v.CreatedAt, deleted, expectedRev)
	if err != nil {
		return fmt.Errorf("cas update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish conflict from absence without leaking foreign documents.
		if _, err := s.Get(ctx, ownerID, id); err != nil {
			return remotedocs.ErrNotFound
		}
		return remotedocs.ErrRevisionConflict
	}
	if err := insertVersion(ctx, tx, v); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertVersion(ctx context.Context, tx pgx.Tx, v remotedocs.Version) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO document_versions(document_id, revision, operation, title, source_url,
		                              sha256, blob_key, size_bytes, mime_type, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		v.DocumentID, v.Revision, v.Operation, v.Title, v.SourceURL,
		v.SHA256, v.BlobKey, v.SizeBytes, v.MimeType, v.CreatedAt); err != nil {
		return fmt.Errorf("insert version: %w", err)
	}
	return nil
}
