package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Store) ResolveTopic(ctx context.Context, selector string) (*catalog.Document, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, "", errors.New("topic selector is required")
	}
	records, err := s.topicDocumentRecords(ctx, ` WHERE l.relative_path LIKE 'topic-%.md' AND l.status != 'untracked' AND `+notTrashedClause+` AND (d.id=? OR lower(l.relative_path)=lower(?) OR lower(COALESCE(i.title,''))=lower(?) OR lower(REPLACE(COALESCE(i.title,''),' ','-'))=lower(?))`, selector, "topic-"+topicFileSlug(selector)+".md", selector, selector)
	if err != nil {
		return nil, "", err
	}
	if len(records) == 0 {
		return nil, "", fmt.Errorf("topic %q not found", selector)
	}
	if len(records) > 1 {
		return nil, "", fmt.Errorf("topic selector %q is ambiguous", selector)
	}
	return records[0].Document, records[0].AbsolutePath, nil
}

func (s *Store) ListTopics(ctx context.Context) ([]port.DocumentRecord, error) {
	return s.topicDocumentRecords(ctx, ` WHERE l.relative_path LIKE 'topic-%.md' AND l.status != 'untracked' AND `+notTrashedClause)
}

func (s *Store) topicDocumentRecords(ctx context.Context, where string, arguments ...any) ([]port.DocumentRecord, error) {
	rows, err := s.db.QueryContext(ctx, documentSelect+where+` ORDER BY lower(COALESCE(i.title,'')), lower(l.relative_path), d.id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("listing topic documents: %w", err)
	}
	defer rows.Close()
	var records []port.DocumentRecord
	for rows.Next() {
		document, absolutePath, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		if _, isTopic := catalog.TopicNameForDocument(document); !isTopic {
			continue
		}
		records = append(records, port.DocumentRecord{Document: document, AbsolutePath: absolutePath})
	}
	return records, rows.Err()
}

func topicFileSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func (s *Store) AddEdge(ctx context.Context, edge catalog.GraphEdge) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO graph_edges(from_document_id,to_document_id,kind,created_at,updated_at)
VALUES(?,?,?,?,?) ON CONFLICT(from_document_id,to_document_id,kind) DO NOTHING`,
		edge.FromDocumentID, edge.ToDocumentID, edge.Kind, millis(edge.CreatedAt), millis(edge.UpdatedAt))
	if err != nil {
		return false, fmt.Errorf("saving graph edge: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *Store) RemoveEdge(ctx context.Context, fromDocumentID, toDocumentID catalog.DocumentID, kind catalog.EdgeKind) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM graph_edges WHERE from_document_id=? AND to_document_id=? AND kind=?`, fromDocumentID, toDocumentID, kind)
	if err != nil {
		return false, fmt.Errorf("removing graph edge: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *Store) GetDocumentGraph(ctx context.Context, documentID catalog.DocumentID) ([]port.DocumentRecord, []port.DocumentRecord, []port.DocumentRecord, error) {
	outgoing, err := s.documentsByIDs(ctx, `SELECT e.to_document_id FROM graph_edges e WHERE e.from_document_id=? AND e.kind='manual' ORDER BY e.created_at, e.to_document_id`, documentID)
	if err != nil {
		return nil, nil, nil, err
	}
	incoming, err := s.documentsByIDs(ctx, `SELECT e.from_document_id FROM graph_edges e WHERE e.to_document_id=? AND e.kind='manual' ORDER BY e.created_at, e.from_document_id`, documentID)
	if err != nil {
		return nil, nil, nil, err
	}
	topics, err := s.documentsByIDs(ctx, `SELECT e.to_document_id FROM graph_edges e WHERE e.from_document_id=? AND e.kind='member' ORDER BY e.created_at, e.to_document_id`, documentID)
	if err != nil {
		return nil, nil, nil, err
	}
	return outgoing, incoming, topics, nil
}

func (s *Store) documentsByIDs(ctx context.Context, query string, argument any) ([]port.DocumentRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, argument)
	if err != nil {
		return nil, fmt.Errorf("listing graph document IDs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	trashed, err := s.trashedDocumentSet(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]port.DocumentRecord, 0, len(ids))
	for _, id := range ids {
		if trashed[id] {
			continue
		}
		document, absolutePath, err := s.ResolveDocument(ctx, id)
		if err != nil {
			return nil, err
		}
		records = append(records, port.DocumentRecord{Document: document, AbsolutePath: absolutePath})
	}
	return records, nil
}

func (s *Store) ListTopicDocuments(ctx context.Context, topicDocumentID catalog.DocumentID) ([]port.DocumentRecord, error) {
	return s.documentsByIDs(ctx, `SELECT e.from_document_id FROM graph_edges e WHERE e.to_document_id=? AND e.kind='member' ORDER BY e.created_at, e.from_document_id`, topicDocumentID)
}

func (s *Store) trashedDocumentSet(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document_id FROM document_trash`)
	if err != nil {
		return nil, fmt.Errorf("listing trashed documents: %w", err)
	}
	defer rows.Close()
	set := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// TrashDocument records a document as soft-deleted. The caller has already
// moved the file into the path's trash directory and re-pointed its location.
func (s *Store) TrashDocument(ctx context.Context, documentID catalog.DocumentID, originRelativePath string, trashedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_trash(document_id,origin_relative_path,trashed_at) VALUES(?,?,?)
ON CONFLICT(document_id) DO UPDATE SET origin_relative_path=excluded.origin_relative_path,trashed_at=excluded.trashed_at`,
		documentID, originRelativePath, millis(trashedAt))
	if err != nil {
		return fmt.Errorf("recording trashed document %s: %w", documentID, err)
	}
	return nil
}

// GetTrashedDocument returns the trash record for a document, if any.
func (s *Store) GetTrashedDocument(ctx context.Context, documentID catalog.DocumentID) (port.TrashRecord, bool, error) {
	var record port.TrashRecord
	var trashedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT document_id,origin_relative_path,trashed_at FROM document_trash WHERE document_id=?`, documentID).
		Scan(&record.DocumentID, &record.OriginRelativePath, &trashedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.TrashRecord{}, false, nil
	}
	if err != nil {
		return port.TrashRecord{}, false, fmt.Errorf("reading trash record %s: %w", documentID, err)
	}
	record.TrashedAt = fromMillis(trashedAt)
	return record, true, nil
}

// DeleteTrashRecord forgets a trash entry (used on restore and purge).
func (s *Store) DeleteTrashRecord(ctx context.Context, documentID catalog.DocumentID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM document_trash WHERE document_id=?`, documentID); err != nil {
		return fmt.Errorf("clearing trash record %s: %w", documentID, err)
	}
	return nil
}

// SetDocumentRelativePath re-points a document's location within its path
// (trash move / restore) without touching index state.
func (s *Store) SetDocumentRelativePath(ctx context.Context, documentID catalog.DocumentID, relativePath string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE document_locations SET relative_path=? WHERE document_id=?`, relativePath, documentID); err != nil {
		return fmt.Errorf("relocating document %s: %w", documentID, err)
	}
	return nil
}

// PurgeDocument removes a document and every child row, including its FTS
// entry. The caller has already deleted the file on disk.
func (s *Store) PurgeDocument(ctx context.Context, documentID catalog.DocumentID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_fts WHERE document_id=?`, documentID); err != nil {
		return fmt.Errorf("purging FTS entry %s: %w", documentID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id=?`, documentID); err != nil {
		return fmt.Errorf("purging document %s: %w", documentID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing purge of %s: %w", documentID, err)
	}
	s.invalidateLogicalIDs()
	return nil
}

// ListTrashedDocuments returns every soft-deleted document with its current
// (trash) location and index state.
func (s *Store) ListTrashedDocuments(ctx context.Context) ([]port.DocumentRecord, []port.TrashRecord, error) {
	rows, err := s.db.QueryContext(ctx, documentSelect+` WHERE `+`d.id IN (SELECT document_id FROM document_trash)`+
		` ORDER BY (SELECT trashed_at FROM document_trash t WHERE t.document_id=d.id), d.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("listing trashed documents: %w", err)
	}
	defer rows.Close()
	var records []port.DocumentRecord
	for rows.Next() {
		document, absolutePath, err := scanDocument(rows)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, port.DocumentRecord{Document: document, AbsolutePath: absolutePath})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	trashRows := make([]port.TrashRecord, 0, len(records))
	for _, record := range records {
		trash, ok, err := s.GetTrashedDocument(ctx, record.Document.ID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		trashRows = append(trashRows, trash)
	}
	return records, trashRows, nil
}
