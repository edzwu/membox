package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Store) Search(ctx context.Context, query string, limit int, exact bool) ([]port.SearchHit, error) {
	ftsQuery := plainFTSQuery(query, exact)
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

// GrepDocuments performs case-insensitive literal substring matching over
// indexed document bodies (plus titles and paths), the way `rg -i` scans
// files. Unlike FTS Search it does not tokenize: camelCase identifiers such
// as ChatServiceImpl match, and multi-word input is one literal needle, not
// an AND of terms. Meant for rare literal keywords (names, identifiers).
func (s *Store) GrepDocuments(ctx context.Context, pattern string, limit int) ([]port.SearchHit, error) {
	needle := strings.TrimSpace(pattern)
	if needle == "" {
		return nil, errors.New("grep pattern is required")
	}
	rows, err := s.db.QueryContext(ctx, `WITH n(v) AS (VALUES(lower(?)))
SELECT f.document_id,f.title,f.path,substr(f.body,max(1,instr(lower(f.body),n.v)-60),160)
FROM document_fts f JOIN document_locations l ON l.document_id=f.document_id, n
WHERE l.status='active'
  AND f.document_id NOT IN (SELECT document_id FROM document_trash)
  AND (instr(lower(f.body),n.v)>0 OR instr(lower(f.title),n.v)>0 OR instr(lower(f.path),n.v)>0)
ORDER BY instr(lower(COALESCE(f.title,'')),n.v)=0, bm25(document_fts,5.0,2.0,1.0)
LIMIT ?`, needle, limit)
	if err != nil {
		return nil, fmt.Errorf("grepping documents: %w", err)
	}
	defer rows.Close()
	var hits []port.SearchHit
	for rows.Next() {
		var hit port.SearchHit
		if err := rows.Scan(&hit.DocumentID, &hit.Title, &hit.Path, &hit.Snippet); err != nil {
			return nil, err
		}
		hit.Snippet = strings.Join(strings.Fields(hit.Snippet), " ")
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// SuggestDocuments performs literal substring matching for picker UIs. UUIDs
// are not part of FTS, so this deliberately searches document identity and
// metadata rather than document bodies. Whitespace-separated terms are ANDed:
// each term must hit id, title, or relative_path somewhere (mirrors the TUI
// name tags' AND semantics without an in-memory list page).
func (s *Store) SuggestDocuments(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return nil, errors.New("suggestion query is empty")
	}
	conditions := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)*3+1)
	for _, term := range terms {
		conditions = append(conditions,
			"(instr(lower(d.id),?)>0 OR instr(lower(COALESCE(i.title,'')),?)>0 OR instr(lower(l.relative_path),?)>0)")
		args = append(args, term, term, term)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,COALESCE(i.title,''),l.relative_path,''
FROM documents d
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
WHERE l.status='active' AND `+notTrashedClause+`
  AND `+strings.Join(conditions, " AND ")+`
ORDER BY lower(COALESCE(i.title,'')),lower(l.relative_path)
LIMIT ?`, args...)
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

func plainFTSQuery(query string, exact bool) string {
	fields := strings.Fields(query)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, `"`, `""`)
		if field != "" {
			// Prefix wildcard by default ("wal" hits "wall") for forgiving
			// content search; exact matching quotes the token with no `*` so
			// whole-token hits only ("wal" then excludes "wall").
			if exact {
				parts = append(parts, `"`+field+`"`)
			} else {
				parts = append(parts, `"`+field+`"*`)
			}
		}
	}
	return strings.Join(parts, " ")
}

func (s *Store) ListDocuments(ctx context.Context, limit int, includeUnavailable bool, statusFilter string) ([]port.DocumentRecord, error) {
	where := "WHERE l.status='active' AND " + notTrashedClause
	if includeUnavailable {
		where = "WHERE " + notTrashedClause
	}
	args := []any{limit}
	if statusFilter != "" {
		where += " AND COALESCE(r.read_status,'unread')=?"
		args = append([]any{statusFilter}, args...)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.created_at,d.updated_at,d.pinned,l.path_id,l.relative_path,l.file_key,l.status,
COALESCE(i.title,''),COALESCE(i.summary,''),COALESCE(i.media_type,'text/markdown'),COALESCE(i.metadata_overrides,0),COALESCE(i.authors,''),
COALESCE(i.publication_year,0),COALESCE(i.keywords,''),COALESCE(i.page_count,0),
COALESCE(i.mtime,0),COALESCE(i.size,0),COALESCE(i.sha256,''),i.indexed_at,
COALESCE(i.source_created_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.created_at END),
MAX(COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),
    COALESCE((SELECT MAX(MAX(an.updated_at,COALESCE(ni.source_updated_at,0)))
              FROM annotation_notes an
              LEFT JOIN document_index ni ON ni.document_id=an.note_document_id
              WHERE an.target_document_id=d.id
                AND an.note_document_id NOT IN (SELECT document_id FROM document_trash)),0)),p.root_path,
COALESCE(r.read_status,'unread')
FROM documents d JOIN document_locations l ON l.document_id=d.id
JOIN paths p ON p.id=l.path_id LEFT JOIN document_index i ON i.document_id=d.id
LEFT JOIN document_read_state r ON r.document_id=d.id`+` `+where+` ORDER BY lower(COALESCE(i.title,'')), lower(l.relative_path), d.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()
	var records []port.DocumentRecord
	for rows.Next() {
		var readStatus string
		// Feed the trailing read_status through a wrapper so the shared
		// document projection and caller-specific field land in one Scan.
		document, absolutePath, err := scanDocument(&rowWithExtra{rows: rows, extra: &readStatus})
		if err != nil {
			return nil, err
		}
		records = append(records, port.DocumentRecord{Document: document, AbsolutePath: absolutePath, ReadStatus: readStatus})
	}
	return records, rows.Err()
}

// rowWithExtra appends one extra destination to rows.Scan, letting shared
// scanDocument read N columns while the caller receives one more.
type rowWithExtra struct {
	rows  *sql.Rows
	extra *string
}

func (r *rowWithExtra) Scan(dest ...any) error {
	dest = append(dest, r.extra)
	return r.rows.Scan(dest...)
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
