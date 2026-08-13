package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"membox/internal/application/port"
)

func (s *Store) IngestResources(ctx context.Context, resources []port.ResourceInsert) ([]port.ResourceIngestRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]port.ResourceIngestRecord, 0, len(resources))
	for _, input := range resources {
		createdAt := millis(input.CreatedAt)
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO resources
(id,url,canonical_url,title,source_document_id,source_file,source_line,source_commit,created_at,updated_at)
VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?,?)`,
			input.ID, input.URL, input.CanonicalURL, input.Title, input.SourceDocumentID,
			input.SourceFile, input.SourceLine, input.SourceCommit, createdAt, createdAt)
		if err != nil {
			return nil, fmt.Errorf("ingesting resource %q: %w", input.CanonicalURL, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		inserted := rows > 0
		if !inserted && input.Title != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE resources
SET title=CASE WHEN title='' THEN ? ELSE title END,
    updated_at=CASE WHEN title='' THEN ? ELSE updated_at END
WHERE canonical_url=?`, input.Title, createdAt, input.CanonicalURL); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO resource_sources
(resource_id,source_document_id,source_file,source_line,source_commit,created_at)
SELECT id,NULLIF(?,''),?,?,?,? FROM resources WHERE canonical_url=?`,
			input.SourceDocumentID, input.SourceFile, input.SourceLine, input.SourceCommit,
			createdAt, input.CanonicalURL); err != nil {
			return nil, fmt.Errorf("saving resource provenance %q: %w", input.CanonicalURL, err)
		}
		record, err := scanResource(tx.QueryRowContext(ctx, resourceSelect+` WHERE canonical_url=?`, input.CanonicalURL))
		if err != nil {
			return nil, err
		}
		out = append(out, port.ResourceIngestRecord{Resource: record, Inserted: inserted})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) AssessResources(ctx context.Context, assessments []port.ResourceAssessment) ([]port.ResourceRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]port.ResourceRecord, 0, len(assessments))
	for _, assessment := range assessments {
		result, err := tx.ExecContext(ctx, `UPDATE resources SET priority=?,score=?,reason=?,updated_at=? WHERE id=?`,
			assessment.Priority, assessment.Score, assessment.Reason, millis(assessment.UpdatedAt), assessment.ID)
		if err != nil {
			return nil, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			return nil, fmt.Errorf("no resource found: %s", assessment.ID)
		}
		record, err := scanResource(tx.QueryRowContext(ctx, resourceSelect+` WHERE id=?`, assessment.ID))
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListResources(ctx context.Context, limit int) ([]port.ResourceRecord, error) {
	return s.listResourcesQuery(ctx, resourceSelect+`
ORDER BY CASE priority WHEN 'H' THEN 0 WHEN 'M' THEN 1 ELSE 2 END,
         score DESC, created_at DESC, id DESC
LIMIT ?`, limit)
}

func (s *Store) ListResourcesBySource(ctx context.Context, sourceDocumentID, sourceFile string, limit int) ([]port.ResourceRecord, error) {
	if sourceDocumentID != "" {
		return s.listResourcesQuery(ctx, resourceSelect+`
WHERE EXISTS (SELECT 1 FROM resource_sources rs WHERE rs.resource_id=resources.id AND rs.source_document_id=?)
ORDER BY CASE priority WHEN 'H' THEN 0 WHEN 'M' THEN 1 ELSE 2 END,
         score DESC, created_at DESC, id DESC
LIMIT ?`, sourceDocumentID, limit)
	}
	return s.listResourcesQuery(ctx, resourceSelect+`
WHERE EXISTS (SELECT 1 FROM resource_sources rs WHERE rs.resource_id=resources.id AND rs.source_file=?)
ORDER BY CASE priority WHEN 'H' THEN 0 WHEN 'M' THEN 1 ELSE 2 END,
         score DESC, created_at DESC, id DESC
LIMIT ?`, sourceFile, limit)
}

func (s *Store) listResourcesQuery(ctx context.Context, query string, args ...any) ([]port.ResourceRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]port.ResourceRecord, 0)
	for rows.Next() {
		record, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ResourceScanState(ctx context.Context, source string) (int, error) {
	var wave int
	err := s.db.QueryRowContext(ctx, `SELECT wave FROM resource_scan WHERE source=?`, source).Scan(&wave)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return wave, err
}

func (s *Store) SetResourceScanState(ctx context.Context, source string, wave int, reset bool, now time.Time) error {
	if reset {
		_, err := s.db.ExecContext(ctx, `INSERT INTO resource_scan(source,wave,updated_at) VALUES(?,0,?)
ON CONFLICT(source) DO UPDATE SET wave=0,updated_at=excluded.updated_at`, source, millis(now))
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_scan(source,wave,updated_at) VALUES(?,?,?)
ON CONFLICT(source) DO UPDATE SET wave=MAX(resource_scan.wave,excluded.wave),updated_at=excluded.updated_at`,
		source, wave, millis(now))
	return err
}

const resourceSelect = `SELECT id,url,canonical_url,title,priority,score,reason,
COALESCE(source_document_id,''),source_file,source_line,source_commit,created_at,updated_at FROM resources`

type rowScanner interface{ Scan(dest ...any) error }

func scanResource(row rowScanner) (port.ResourceRecord, error) {
	var out port.ResourceRecord
	var score sql.NullFloat64
	var createdAt, updatedAt int64
	if err := row.Scan(&out.ID, &out.URL, &out.CanonicalURL, &out.Title, &out.Priority,
		&score, &out.Reason, &out.SourceDocumentID, &out.SourceFile, &out.SourceLine,
		&out.SourceCommit, &createdAt, &updatedAt); err != nil {
		return out, err
	}
	if score.Valid {
		value := score.Float64
		out.Score = &value
	}
	out.CreatedAt = fromMillis(createdAt)
	out.UpdatedAt = fromMillis(updatedAt)
	return out, nil
}
