package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Store) ListDocumentSources(ctx context.Context, clipMode string) ([]port.DocumentSourceRecord, error) {
	query := documentSelect + `
JOIN document_sources ds ON ds.document_id = d.id
WHERE l.status='active' AND ` + notTrashedClause + `
`
	var args []any
	if clipMode != "" {
		query += ` AND ds.clip_mode=?`
		args = append(args, clipMode)
	}
	query += ` ORDER BY ds.created_at, d.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing document sources: %w", err)
	}
	defer rows.Close()
	var out []port.DocumentSourceRecord
	for rows.Next() {
		document, absolutePath, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		var mode, sourceURL string
		_ = s.db.QueryRowContext(ctx, `SELECT clip_mode, source_url FROM document_sources WHERE document_id=?`, string(document.ID)).
			Scan(&mode, &sourceURL)
		out = append(out, port.DocumentSourceRecord{
			DocumentID:   document.ID,
			Title:        document.Index.Title,
			AbsolutePath: absolutePath,
			RelativePath: document.Location.RelativePath,
			ClipMode:     mode,
			SourceURL:    sourceURL,
		})
	}
	return out, rows.Err()
}

func (s *Store) UpsertDocumentSource(ctx context.Context, documentID catalog.DocumentID, sourceURLNorm, clipMode string, now time.Time) error {
	sourceURLNorm = strings.TrimSpace(sourceURLNorm)
	if documentID == "" || sourceURLNorm == "" {
		return fmt.Errorf("document id and source url are required")
	}
	clipMode = strings.TrimSpace(clipMode)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO document_sources(document_id, source_url, source_url_norm, clip_mode, created_at)
VALUES(?,?,?,?,?)
ON CONFLICT(document_id) DO UPDATE SET
  source_url=excluded.source_url,
  source_url_norm=excluded.source_url_norm,
  clip_mode=excluded.clip_mode
`, string(documentID), sourceURLNorm, sourceURLNorm, clipMode, now.UTC().Unix())
	if err != nil {
		return fmt.Errorf("upserting document source: %w", err)
	}
	return nil
}

func (s *Store) ListDocumentsBySourceURL(ctx context.Context, sourceURLNorm string, selectionNotesOnly bool) ([]port.DocumentSourceRecord, error) {
	sourceURLNorm = strings.TrimSpace(sourceURLNorm)
	if sourceURLNorm == "" {
		return nil, nil
	}
	query := documentSelect + `
JOIN document_sources ds ON ds.document_id = d.id
WHERE l.status='active' AND ` + notTrashedClause + ` AND ds.source_url_norm=?
`
	args := []any{sourceURLNorm}
	if selectionNotesOnly {
		// Explicit selection clips, or legacy files named *-note.md.
		query += ` AND (ds.clip_mode='selection' OR l.relative_path LIKE '%-note.md')`
	}
	query += ` ORDER BY ds.created_at DESC, d.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing documents by source url: %w", err)
	}
	defer rows.Close()
	var out []port.DocumentSourceRecord
	for rows.Next() {
		document, absolutePath, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		// Re-read clip metadata (scanDocument does not include sources).
		var clipMode, sourceURL string
		_ = s.db.QueryRowContext(ctx, `SELECT clip_mode, source_url FROM document_sources WHERE document_id=?`, string(document.ID)).
			Scan(&clipMode, &sourceURL)
		out = append(out, port.DocumentSourceRecord{
			DocumentID:   document.ID,
			Title:        document.Index.Title,
			AbsolutePath: absolutePath,
			RelativePath: document.Location.RelativePath,
			ClipMode:     clipMode,
			SourceURL:    sourceURL,
		})
	}
	return out, rows.Err()
}

func (s *Store) AddOrReactivatePath(ctx context.Context, root string, now time.Time) (*catalog.IndexedPath, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	path, err := loadPathByRoot(ctx, tx, root)
	if err == nil {
		if path.Status == catalog.PathRemoved {
			path.Activate()
			if _, err := tx.ExecContext(ctx, `UPDATE paths SET status='ready',last_error='' WHERE id=?`, path.ID); err != nil {
				return nil, false, err
			}
			if err := tx.Commit(); err != nil {
				return nil, false, err
			}
			return path, false, nil
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return path, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO paths(root_path,created_at,status) VALUES(?,?,'ready')`, root, millis(now))
	if err != nil {
		return nil, false, fmt.Errorf("adding path: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	path, err = catalog.RehydrateIndexedPath(catalog.IndexedPathID(id), root, catalog.PathReady, now, nil, "")
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return path, false, nil
}

func (s *Store) ListPaths(ctx context.Context, includeRemoved bool) ([]port.PathSummary, error) {
	where := `WHERE p.status != 'removed'`
	if includeRemoved {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.root_path,p.status,p.created_at,p.last_scan_at,p.last_error,
COUNT(l.document_id) FROM paths p LEFT JOIN document_locations l ON l.path_id=p.id `+where+`
GROUP BY p.id ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("listing paths: %w", err)
	}
	defer rows.Close()
	var out []port.PathSummary
	for rows.Next() {
		path, count, err := scanPathSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, port.PathSummary{Path: *path, DocumentCount: count})
	}
	return out, rows.Err()
}

func scanPathSummary(scanner interface{ Scan(...any) error }) (*catalog.IndexedPath, int, error) {
	var id int64
	var root, status, lastError string
	var created int64
	var last sql.NullInt64
	var count int
	if err := scanner.Scan(&id, &root, &status, &created, &last, &lastError, &count); err != nil {
		return nil, 0, err
	}
	var lastTime *time.Time
	if last.Valid {
		value := fromMillis(last.Int64)
		lastTime = &value
	}
	path, err := catalog.RehydrateIndexedPath(catalog.IndexedPathID(id), root, catalog.PathStatus(status), fromMillis(created), lastTime, lastError)
	return path, count, err
}

func (s *Store) ResolvePath(ctx context.Context, selector string, includeRemoved bool) (*catalog.IndexedPath, error) {
	selector = strings.TrimSpace(selector)
	where := ""
	if !includeRemoved {
		where = ` AND status != 'removed'`
	}
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil && id > 0 {
		row := s.db.QueryRowContext(ctx, `SELECT id,root_path,status,created_at,last_scan_at,last_error,0 FROM paths WHERE id=?`+where, id)
		path, _, err := scanPathSummary(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("path %q not found", selector)
		}
		return path, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id,root_path,status,created_at,last_scan_at,last_error,0 FROM paths WHERE root_path=?`+where, selector)
	path, _, err := scanPathSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("path %q not found", selector)
	}
	return path, err
}

func (s *Store) RemovePath(ctx context.Context, id catalog.IndexedPathID, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE paths SET status='removed' WHERE id=?`, id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE document_locations SET status='untracked',last_seen_at=? WHERE path_id=?`, millis(now), id)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *Store) DocumentsForPath(ctx context.Context, id catalog.IndexedPathID) ([]*catalog.Document, error) {
	rows, err := s.db.QueryContext(ctx, documentSelect+` WHERE l.path_id=? ORDER BY l.relative_path`, id)
	if err != nil {
		return nil, fmt.Errorf("loading path documents: %w", err)
	}
	defer rows.Close()
	var out []*catalog.Document
	for rows.Next() {
		doc, _, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

// notTrashedClause hides soft-deleted documents from listings while keeping
// their rows intact for restore.
