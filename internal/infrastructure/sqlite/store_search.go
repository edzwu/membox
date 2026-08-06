package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Store) Search(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
	ftsQuery := plainFTSQuery(query)
	if ftsQuery == "" {
		return nil, errors.New("search query is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.document_id,f.title,f.path,snippet(document_fts,3,'','','…',24)
FROM document_fts f JOIN document_locations l ON l.document_id=f.document_id
WHERE document_fts MATCH ? AND l.status='active'
  AND f.document_id NOT IN (SELECT document_id FROM document_trash)
ORDER BY bm25(document_fts,5.0,2.0,1.0) LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("searching documents: %w", err)
	}
	defer rows.Close()
	var hits []port.SearchHit
	for rows.Next() {
		var hit port.SearchHit
		if err := rows.Scan(&hit.DocumentID, &hit.Title, &hit.Path, &hit.Snippet); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// SuggestDocuments performs literal substring matching for picker UIs. UUIDs
// are not part of FTS, so this deliberately searches document identity and
// metadata rather than document bodies.
func (s *Store) SuggestDocuments(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `WITH needle(value) AS (VALUES(lower(?)))
SELECT d.id,COALESCE(i.title,''),l.relative_path,''
FROM documents d
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
CROSS JOIN needle n
WHERE l.status='active' AND `+notTrashedClause+`
  AND (instr(lower(d.id),n.value)>0 OR instr(lower(COALESCE(i.title,'')),n.value)>0 OR instr(lower(l.relative_path),n.value)>0)
ORDER BY CASE
  WHEN lower(d.id)=n.value THEN 0
  WHEN instr(lower(d.id),n.value)=1 THEN 1
  WHEN instr(lower(COALESCE(i.title,'')),n.value)=1 THEN 2
  WHEN instr(lower(d.id),n.value)>0 THEN 3
  WHEN instr(lower(COALESCE(i.title,'')),n.value)>0 THEN 4
  ELSE 5 END,
  lower(COALESCE(i.title,'')),lower(l.relative_path)
LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("suggesting documents: %w", err)
	}
	defer rows.Close()
	var hits []port.SearchHit
	for rows.Next() {
		var hit port.SearchHit
		if err := rows.Scan(&hit.DocumentID, &hit.Title, &hit.Path, &hit.Snippet); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func plainFTSQuery(query string) string {
	fields := strings.Fields(query)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, `"`, `""`)
		if field != "" {
			parts = append(parts, `"`+field+`"*`)
		}
	}
	return strings.Join(parts, " ")
}

func (s *Store) ListDocuments(ctx context.Context, limit int, includeUnavailable bool) ([]port.DocumentRecord, error) {
	where := "WHERE l.status='active' AND " + notTrashedClause
	if includeUnavailable {
		where = "WHERE " + notTrashedClause
	}
	rows, err := s.db.QueryContext(ctx, documentSelect+` `+where+` ORDER BY lower(COALESCE(i.title,'')), lower(l.relative_path), d.id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()
	var records []port.DocumentRecord
	for rows.Next() {
		document, absolutePath, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, port.DocumentRecord{Document: document, AbsolutePath: absolutePath})
	}
	return records, rows.Err()
}

func (s *Store) ResolveDocument(ctx context.Context, selector string) (*catalog.Document, string, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if !selectorPattern.MatchString(selector) {
		return nil, "", fmt.Errorf("invalid document selector %q", selector)
	}
	// UI short IDs are the *last* 4 hex chars (see host.ShortDocumentID).
	// Prefer prefix match (full/partial UUID), then unique suffix match.
	docs, paths, err := s.queryDocumentsByIDPattern(ctx, selector+"%")
	if err != nil {
		return nil, "", err
	}
	if len(docs) == 0 {
		docs, paths, err = s.queryDocumentsByIDPattern(ctx, "%"+selector)
		if err != nil {
			return nil, "", err
		}
	}
	if len(docs) == 0 {
		return nil, "", fmt.Errorf("document %q not found", selector)
	}
	if len(docs) > 1 {
		return nil, "", fmt.Errorf("document selector %q is ambiguous; use a longer id", selector)
	}
	return docs[0], paths[0], nil
}

func (s *Store) queryDocumentsByIDPattern(ctx context.Context, pattern string) ([]*catalog.Document, []string, error) {
	rows, err := s.db.QueryContext(ctx, documentSelect+` WHERE lower(d.id) LIKE ? ORDER BY d.id LIMIT 2`, pattern)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var docs []*catalog.Document
	var paths []string
	for rows.Next() {
		doc, path, err := scanDocument(rows)
		if err != nil {
			return nil, nil, err
		}
		docs, paths = append(docs, doc), append(paths, path)
	}
	return docs, paths, rows.Err()
}
