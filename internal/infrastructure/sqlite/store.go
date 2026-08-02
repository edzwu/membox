package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

var selectorPattern = regexp.MustCompile(`^[0-9a-fA-F-]+$`)

type Store struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating membox home: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening SQLite: %w", err)
	}
	db.SetMaxOpenConns(8)
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configuring SQLite: %w", err)
		}
	}
	store := &Store{db: db, path: path}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS paths (
    id INTEGER PRIMARY KEY,
    root_path TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    last_scan_at INTEGER,
    status TEXT NOT NULL CHECK(status IN ('ready','partial','removed')),
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS document_locations (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    path_id INTEGER NOT NULL REFERENCES paths(id),
    relative_path TEXT NOT NULL,
    file_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('active','missing','untracked')),
    last_seen_at INTEGER,
    UNIQUE(path_id, relative_path)
);
CREATE TABLE IF NOT EXISTS document_index (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    mtime INTEGER NOT NULL DEFAULT 0,
    size INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    indexed_at INTEGER
);
CREATE VIRTUAL TABLE IF NOT EXISTS document_fts USING fts5(
    document_id UNINDEXED,
    title,
    path,
    body
);
CREATE INDEX IF NOT EXISTS locations_path_status ON document_locations(path_id, status);
CREATE INDEX IF NOT EXISTS locations_status ON document_locations(status);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating SQLite: %w", err)
	}
	// Migration: add summary column to document_index if missing (for databases created before summary was added)
	var hasSummary bool
	rows, err := s.db.Query(`PRAGMA table_info(document_index)`)
	if err != nil {
		return fmt.Errorf("checking document_index columns: %w", err)
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scanning table_info: %w", err)
		}
		if name == "summary" {
			hasSummary = true
			break
		}
	}
	rows.Close()
	if !hasSummary {
		if _, err := s.db.Exec(`ALTER TABLE document_index ADD COLUMN summary TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("adding summary column: %w", err)
		}
	}
	return nil
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

const documentSelect = `SELECT d.id,d.created_at,d.updated_at,l.path_id,l.relative_path,l.file_key,l.status,
COALESCE(i.title,''),COALESCE(i.summary,''),COALESCE(i.mtime,0),COALESCE(i.size,0),COALESCE(i.sha256,''),i.indexed_at,p.root_path
FROM documents d JOIN document_locations l ON l.document_id=d.id
JOIN paths p ON p.id=l.path_id LEFT JOIN document_index i ON i.document_id=d.id`

func scanDocument(scanner interface{ Scan(...any) error }) (*catalog.Document, string, error) {
	var id, relative, fileKey, status, title, summary, hash, root string
	var created, updated, pathID, mtime, size int64
	var indexed sql.NullInt64
	if err := scanner.Scan(&id, &created, &updated, &pathID, &relative, &fileKey, &status, &title, &summary, &mtime, &size, &hash, &indexed, &root); err != nil {
		return nil, "", err
	}
	location, err := catalog.NewLocation(catalog.IndexedPathID(pathID), relative)
	if err != nil {
		return nil, "", err
	}
	var indexedAt time.Time
	if indexed.Valid {
		indexedAt = fromMillis(indexed.Int64)
	}
	doc, err := catalog.RehydrateDocument(catalog.DocumentID(id), location, catalog.FileKey(fileKey), catalog.DocumentStatus(status), catalog.IndexState{Title: title, Summary: summary, MTime: mtime, Size: size, SHA256: hash, IndexedAt: indexedAt}, fromMillis(created), fromMillis(updated))
	if err != nil {
		return nil, "", fmt.Errorf("rehydrating document %q: %w", id, err)
	}
	return doc, filepath.Join(root, filepath.FromSlash(relative)), nil
}

func (s *Store) SaveScan(ctx context.Context, indexedPath *catalog.IndexedPath, saves []port.ScanSave) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := savePath(ctx, tx, indexedPath); err != nil {
		return err
	}
	for _, save := range saves {
		if err := saveDocument(ctx, tx, save); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing scan: %w", err)
	}
	return nil
}

func (s *Store) SaveDocument(ctx context.Context, save port.ScanSave) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveDocument(ctx, tx, save); err != nil {
		return err
	}
	return tx.Commit()
}

func savePath(ctx context.Context, tx *sql.Tx, path *catalog.IndexedPath) error {
	var last any
	if path.LastScanAt != nil {
		last = millis(*path.LastScanAt)
	}
	_, err := tx.ExecContext(ctx, `UPDATE paths SET status=?,last_scan_at=?,last_error=? WHERE id=?`, path.Status, last, path.LastError, path.ID)
	return err
}

func saveDocument(ctx context.Context, tx *sql.Tx, save port.ScanSave) error {
	d := save.Document
	if d == nil {
		return errors.New("cannot save nil document")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents(id,created_at,updated_at) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at`, d.ID, millis(d.CreatedAt), millis(d.UpdatedAt)); err != nil {
		return fmt.Errorf("saving document: %w", err)
	}
	lastSeen := any(nil)
	if d.Status == catalog.DocumentActive {
		lastSeen = millis(d.UpdatedAt)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_locations(document_id,path_id,relative_path,file_key,status,last_seen_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(document_id) DO UPDATE SET path_id=excluded.path_id,relative_path=excluded.relative_path,
file_key=excluded.file_key,status=excluded.status,last_seen_at=excluded.last_seen_at`,
		d.ID, d.Location.PathID, d.Location.RelativePath, d.FileKey, d.Status, lastSeen); err != nil {
		return fmt.Errorf("saving document location: %w", err)
	}
	if !save.Reindex {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_index(document_id,title,summary,mtime,size,sha256,indexed_at)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(document_id) DO UPDATE SET title=excluded.title,summary=excluded.summary,mtime=excluded.mtime,size=excluded.size,
sha256=excluded.sha256,indexed_at=excluded.indexed_at`, d.ID, d.Index.Title, d.Index.Summary, d.Index.MTime, d.Index.Size, d.Index.SHA256, millis(d.Index.IndexedAt)); err != nil {
		return fmt.Errorf("saving document index: %w", err)
	}
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT root_path FROM paths WHERE id=?`, d.Location.PathID).Scan(&root); err != nil {
		return err
	}
	fullPath := filepath.Join(root, filepath.FromSlash(d.Location.RelativePath))
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_fts WHERE document_id=?`, d.ID); err != nil {
		return fmt.Errorf("removing old search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_fts(document_id,title,path,body) VALUES(?,?,?,?)`, d.ID, d.Index.Title, fullPath, string(save.Body)); err != nil {
		return fmt.Errorf("updating search index: %w", err)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]port.SearchHit, error) {
	ftsQuery := plainFTSQuery(query)
	if ftsQuery == "" {
		return nil, errors.New("search query is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.document_id,f.title,f.path,snippet(document_fts,3,'','','…',24)
FROM document_fts f JOIN document_locations l ON l.document_id=f.document_id
WHERE document_fts MATCH ? AND l.status='active'
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
	where := "WHERE l.status='active'"
	if includeUnavailable {
		where = ""
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
	rows, err := s.db.QueryContext(ctx, documentSelect+` WHERE lower(d.id) LIKE ? ORDER BY d.id LIMIT 2`, selector+"%")
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var docs []*catalog.Document
	var paths []string
	for rows.Next() {
		doc, path, err := scanDocument(rows)
		if err != nil {
			return nil, "", err
		}
		docs, paths = append(docs, doc), append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(docs) == 0 {
		return nil, "", fmt.Errorf("document %q not found", selector)
	}
	if len(docs) > 1 {
		return nil, "", fmt.Errorf("document selector %q is ambiguous; use a longer prefix", selector)
	}
	return docs[0], paths[0], nil
}

func (s *Store) Status(ctx context.Context) (port.StatusSnapshot, error) {
	var status port.StatusSnapshot
	status.DatabasePath = s.path
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM paths WHERE status != 'removed'`).Scan(&status.Paths); err != nil {
		return status, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM document_locations GROUP BY status`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return status, err
		}
		switch catalog.DocumentStatus(name) {
		case catalog.DocumentActive:
			status.Active = count
		case catalog.DocumentMissing:
			status.Missing = count
		case catalog.DocumentUntracked:
			status.Untracked = count
		}
	}
	var last sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(last_scan_at) FROM paths WHERE status != 'removed'`).Scan(&last); err != nil {
		return status, err
	}
	if last.Valid {
		value := fromMillis(last.Int64)
		status.LastScanAt = &value
	}
	return status, nil
}

func loadPathByRoot(ctx context.Context, tx *sql.Tx, root string) (*catalog.IndexedPath, error) {
	row := tx.QueryRowContext(ctx, `SELECT id,root_path,status,created_at,last_scan_at,last_error,0 FROM paths WHERE root_path=?`, root)
	path, _, err := scanPathSummary(row)
	return path, err
}

func millis(t time.Time) int64         { return t.UTC().UnixMilli() }
func fromMillis(value int64) time.Time { return time.UnixMilli(value).UTC() }
