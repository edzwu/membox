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

// GetSetting returns the value for a settings key, or ("", nil) if unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting upserts a settings key/value pair.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		return fmt.Errorf("writing setting %q: %w", key, err)
	}
	return nil
}

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
    updated_at INTEGER NOT NULL,
    pinned INTEGER NOT NULL DEFAULT 0
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
    indexed_at INTEGER,
    source_created_at INTEGER,
    source_updated_at INTEGER
);
CREATE VIRTUAL TABLE IF NOT EXISTS document_fts USING fts5(
    document_id UNINDEXED,
    title,
    path,
    body
);
CREATE INDEX IF NOT EXISTS locations_path_status ON document_locations(path_id, status);
CREATE INDEX IF NOT EXISTS locations_status ON document_locations(status);
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS document_annotations (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    sidecar TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
-- A normalized annotation relation. The selected excerpt and note prose live
-- in note_document_id's Markdown file; SQLite owns UUID relationships and
-- anchoring metadata.
CREATE TABLE IF NOT EXISTS annotation_notes (
    note_document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    target_document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    anchor_start INTEGER NOT NULL DEFAULT 0,
    anchor_prefix TEXT NOT NULL DEFAULT '',
    anchor_suffix TEXT NOT NULL DEFAULT '',
    highlight INTEGER NOT NULL DEFAULT 0,
    underline INTEGER NOT NULL DEFAULT 0,
    strikethrough INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS annotation_notes_target ON annotation_notes(target_document_id);
CREATE TABLE IF NOT EXISTS document_read_state (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    progress_y INTEGER NOT NULL DEFAULT 0,
    progress_at TEXT NOT NULL DEFAULT ''
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating SQLite: %w", err)
	}
	columns := make(map[string]bool)
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
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	migrations := []struct {
		name string
		sql  string
	}{
		{"summary", `ALTER TABLE document_index ADD COLUMN summary TEXT NOT NULL DEFAULT ''`},
		{"source_created_at", `ALTER TABLE document_index ADD COLUMN source_created_at INTEGER`},
		{"source_updated_at", `ALTER TABLE document_index ADD COLUMN source_updated_at INTEGER`},
	}
	for _, migration := range migrations {
		if columns[migration.name] {
			continue
		}
		if _, err := s.db.Exec(migration.sql); err != nil {
			return fmt.Errorf("adding document_index.%s: %w", migration.name, err)
		}
	}
	// Existing databases only have filesystem mtime. Use it as both source
	// dates until an explicit Git sync supplies historical values.
	if _, err := s.db.Exec(`UPDATE document_index SET
source_created_at=COALESCE(source_created_at,CASE WHEN mtime>0 THEN mtime/1000000 ELSE indexed_at END),
source_updated_at=COALESCE(source_updated_at,CASE WHEN mtime>0 THEN mtime/1000000 ELSE indexed_at END)`); err != nil {
		return fmt.Errorf("backfilling document source timestamps: %w", err)
	}
	var hasPinned bool
	documentRows, err := s.db.Query(`PRAGMA table_info(documents)`)
	if err != nil {
		return fmt.Errorf("checking documents columns: %w", err)
	}
	for documentRows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := documentRows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			documentRows.Close()
			return fmt.Errorf("scanning documents table_info: %w", err)
		}
		if name == "pinned" {
			hasPinned = true
		}
	}
	if err := documentRows.Close(); err != nil {
		return err
	}
	if !hasPinned {
		if _, err := s.db.Exec(`ALTER TABLE documents ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("adding documents.pinned: %w", err)
		}
	}
	if err := s.migrateGraphSchema(); err != nil {
		return err
	}
	// Always ensure clip provenance table exists (graph migration may no-op).
	return s.migrateDocumentSources()
}

func (s *Store) migrateGraphSchema() error {
	var nodesTable bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='graph_nodes')`).Scan(&nodesTable); err != nil {
		return fmt.Errorf("checking legacy graph schema: %w", err)
	}
	if nodesTable {
		if _, err := s.db.Exec(`DROP INDEX IF EXISTS graph_nodes_kind_name`); err != nil {
			return fmt.Errorf("dropping legacy graph_nodes index: %w", err)
		}
		if _, err := s.db.Exec(`DROP TABLE graph_nodes`); err != nil {
			return fmt.Errorf("dropping legacy graph_nodes: %w", err)
		}
	}
	var edgesTable bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='graph_edges')`).Scan(&edgesTable); err != nil {
		return fmt.Errorf("checking graph_edges table: %w", err)
	}
	var usesNodeColumns bool
	if edgesTable {
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('graph_edges') WHERE name='from_node_id')`).Scan(&usesNodeColumns); err != nil {
			return fmt.Errorf("checking graph_edges columns: %w", err)
		}
	}
	if edgesTable && !usesNodeColumns {
		if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS graph_edges_from ON graph_edges(from_document_id);
CREATE INDEX IF NOT EXISTS graph_edges_to ON graph_edges(to_document_id)`); err != nil {
			return err
		}
		// Graph already current — still ensure document_sources exists.
		return s.migrateDocumentSources()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if edgesTable {
		if _, err := tx.Exec(`DROP INDEX IF EXISTS graph_edges_from`); err != nil {
			return fmt.Errorf("dropping legacy graph edge index: %w", err)
		}
		if _, err := tx.Exec(`DROP INDEX IF EXISTS graph_edges_to`); err != nil {
			return fmt.Errorf("dropping legacy graph edge index: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE graph_edges RENAME TO graph_edges_old`); err != nil {
			return fmt.Errorf("renaming legacy graph_edges: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE TABLE graph_edges (
from_document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
to_document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
kind TEXT NOT NULL CHECK(kind IN ('manual','member')),
created_at INTEGER NOT NULL,
updated_at INTEGER NOT NULL,
PRIMARY KEY (from_document_id, to_document_id, kind)
)`); err != nil {
		return fmt.Errorf("creating document graph_edges: %w", err)
	}
	if usesNodeColumns {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO graph_edges(from_document_id,to_document_id,kind,created_at,updated_at)
SELECT from_node_id,to_node_id,kind,created_at,updated_at FROM graph_edges_old`); err != nil {
			return fmt.Errorf("copying graph edges: %w", err)
		}
	}
	if edgesTable {
		if _, err := tx.Exec(`DROP TABLE graph_edges_old`); err != nil {
			return fmt.Errorf("dropping legacy graph_edges: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS graph_edges_from ON graph_edges(from_document_id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS graph_edges_to ON graph_edges(to_document_id)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.migrateDocumentSources()
}

// EnsureDocumentSources is exported for tests / recovery; migrate always calls this.

func (s *Store) migrateDocumentSources() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS document_sources (
  document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
  source_url TEXT NOT NULL DEFAULT '',
  source_url_norm TEXT NOT NULL,
  clip_mode TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS document_sources_url_norm ON document_sources(source_url_norm);
CREATE INDEX IF NOT EXISTS document_sources_mode ON document_sources(clip_mode);
`)
	if err != nil {
		return fmt.Errorf("migrating document_sources: %w", err)
	}
	// Soft-delete registry: trashed documents keep their rows and relations;
	// queries hide them and scans skip the trash directory until purge.
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS document_trash (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    origin_relative_path TEXT NOT NULL,
    trashed_at INTEGER NOT NULL
);
`); err != nil {
		return fmt.Errorf("migrating document_trash: %w", err)
	}
	return nil
}

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
WHERE l.status='active' AND ds.source_url_norm=?
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
const notTrashedClause = `d.id NOT IN (SELECT document_id FROM document_trash)`

// documentSelect is the shared read projection for documents. The reported
// source_updated_at is the document's own source time raised to the latest
// annotation-note activity targeting it: taking a note counts as working on
// the annotated document, so newest-sort and date filters see it as modified.
// Both annotation_notes.updated_at (app saves) and the note file's own source
// time (external edits picked up by scans) participate.
const documentSelect = `SELECT d.id,d.created_at,d.updated_at,d.pinned,l.path_id,l.relative_path,l.file_key,l.status,
COALESCE(i.title,''),COALESCE(i.summary,''),COALESCE(i.mtime,0),COALESCE(i.size,0),COALESCE(i.sha256,''),i.indexed_at,
COALESCE(i.source_created_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.created_at END),
MAX(COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),
    COALESCE((SELECT MAX(MAX(an.updated_at,COALESCE(ni.source_updated_at,0)))
              FROM annotation_notes an
              LEFT JOIN document_index ni ON ni.document_id=an.note_document_id
              WHERE an.target_document_id=d.id
                AND an.note_document_id NOT IN (SELECT document_id FROM document_trash)),0)),p.root_path
FROM documents d JOIN document_locations l ON l.document_id=d.id
JOIN paths p ON p.id=l.path_id LEFT JOIN document_index i ON i.document_id=d.id`

func scanDocument(scanner interface{ Scan(...any) error }) (*catalog.Document, string, error) {
	var id, relative, fileKey, status, title, summary, hash, root string
	var created, updated, pinned, pathID, mtime, size, sourceCreated, sourceUpdated int64
	var indexed sql.NullInt64
	if err := scanner.Scan(&id, &created, &updated, &pinned, &pathID, &relative, &fileKey, &status, &title, &summary, &mtime, &size, &hash, &indexed, &sourceCreated, &sourceUpdated, &root); err != nil {
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
	doc, err := catalog.RehydrateDocument(catalog.DocumentID(id), location, catalog.FileKey(fileKey), catalog.DocumentStatus(status), catalog.IndexState{
		Title: title, Summary: summary, MTime: mtime, Size: size, SHA256: hash, IndexedAt: indexedAt,
		SourceCreatedAt: fromMillis(sourceCreated), SourceUpdatedAt: fromMillis(sourceUpdated),
	}, fromMillis(created), fromMillis(updated), pinned != 0)
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

func (s *Store) SaveSourceTimes(ctx context.Context, documents []*catalog.Document) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, document := range documents {
		if document == nil {
			return errors.New("cannot save source times for nil document")
		}
		result, err := tx.ExecContext(ctx, `UPDATE document_index SET source_created_at=?,source_updated_at=? WHERE document_id=?`,
			millis(document.Index.SourceCreatedAt), millis(document.Index.SourceUpdatedAt), document.ID)
		if err != nil {
			return fmt.Errorf("saving source times for document %s: %w", document.ID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("document %s has no index metadata", document.ID)
		}
	}
	return tx.Commit()
}

func (s *Store) SavePinned(ctx context.Context, documentID catalog.DocumentID, pinned bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE documents SET pinned=? WHERE id=?`, pinned, documentID)
	if err != nil {
		return fmt.Errorf("saving pin for document %s: %w", documentID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("document %s not found while saving pin", documentID)
	}
	return nil
}

// SaveAnnotations retains legacy Miru sidecars during migration. New note
// content is stored in Markdown and annotation_notes; empty clears the blob.
func (s *Store) SaveAnnotations(ctx context.Context, documentID catalog.DocumentID, sidecar string) error {
	if strings.TrimSpace(sidecar) == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM document_annotations WHERE document_id=?`, documentID); err != nil {
			return fmt.Errorf("clearing annotations for document %s: %w", documentID, err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_annotations(document_id,sidecar,updated_at) VALUES(?,?,?)
ON CONFLICT(document_id) DO UPDATE SET sidecar=excluded.sidecar,updated_at=excluded.updated_at`,
		documentID, sidecar, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("saving annotations for document %s: %w", documentID, err)
	}
	return nil
}

// GetAnnotations returns a legacy annotation sidecar, or "" if none.
func (s *Store) GetAnnotations(ctx context.Context, documentID catalog.DocumentID) (string, error) {
	var sidecar string
	err := s.db.QueryRowContext(ctx, `SELECT sidecar FROM document_annotations WHERE document_id=?`, documentID).Scan(&sidecar)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading annotations for document %s: %w", documentID, err)
	}
	return sidecar, nil
}

func (s *Store) UpsertAnnotationNote(ctx context.Context, record port.AnnotationNoteRecord) error {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO annotation_notes(
note_document_id,target_document_id,anchor_start,anchor_prefix,anchor_suffix,
highlight,underline,strikethrough,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(note_document_id) DO UPDATE SET
 target_document_id=excluded.target_document_id,
 anchor_start=excluded.anchor_start,
 anchor_prefix=excluded.anchor_prefix,
 anchor_suffix=excluded.anchor_suffix,
 highlight=excluded.highlight,
 underline=excluded.underline,
 strikethrough=excluded.strikethrough,
 updated_at=excluded.updated_at`,
		record.NoteDocumentID, record.TargetDocumentID, record.Start, record.Prefix, record.Suffix,
		record.Highlight, record.Underline, record.Strikethrough, millis(createdAt), millis(updatedAt))
	if err != nil {
		return fmt.Errorf("saving annotation note %s: %w", record.NoteDocumentID, err)
	}
	return nil
}

func (s *Store) ListAnnotationNotes(ctx context.Context, targetDocumentID catalog.DocumentID) ([]port.AnnotationNoteRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.note_document_id,a.target_document_id,a.anchor_start,a.anchor_prefix,a.anchor_suffix,
a.highlight,a.underline,a.strikethrough,a.created_at,a.updated_at
FROM annotation_notes a
JOIN document_locations l ON l.document_id=a.note_document_id
WHERE a.target_document_id=? AND l.status='active'
  AND a.note_document_id NOT IN (SELECT document_id FROM document_trash)
ORDER BY a.anchor_start,a.created_at,a.note_document_id`, targetDocumentID)
	if err != nil {
		return nil, fmt.Errorf("listing annotation notes for %s: %w", targetDocumentID, err)
	}
	defer rows.Close()
	var records []port.AnnotationNoteRecord
	for rows.Next() {
		var record port.AnnotationNoteRecord
		var highlight, underline, strikethrough int
		var createdAt, updatedAt int64
		if err := rows.Scan(&record.NoteDocumentID, &record.TargetDocumentID, &record.Start, &record.Prefix, &record.Suffix,
			&highlight, &underline, &strikethrough, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		record.Highlight = highlight != 0
		record.Underline = underline != 0
		record.Strikethrough = strikethrough != 0
		record.CreatedAt = time.UnixMilli(createdAt).UTC()
		record.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) GetAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) (port.AnnotationNoteRecord, bool, error) {
	var record port.AnnotationNoteRecord
	var highlight, underline, strikethrough int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT note_document_id,target_document_id,anchor_start,anchor_prefix,anchor_suffix,
highlight,underline,strikethrough,created_at,updated_at FROM annotation_notes WHERE note_document_id=?`, noteDocumentID).
		Scan(&record.NoteDocumentID, &record.TargetDocumentID, &record.Start, &record.Prefix, &record.Suffix,
			&highlight, &underline, &strikethrough, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.AnnotationNoteRecord{}, false, nil
	}
	if err != nil {
		return port.AnnotationNoteRecord{}, false, fmt.Errorf("reading annotation note %s: %w", noteDocumentID, err)
	}
	record.Highlight = highlight != 0
	record.Underline = underline != 0
	record.Strikethrough = strikethrough != 0
	record.CreatedAt = time.UnixMilli(createdAt).UTC()
	record.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return record, true, nil
}

func (s *Store) DeleteAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM annotation_notes WHERE note_document_id=?`, noteDocumentID); err != nil {
		return fmt.Errorf("deleting annotation note %s: %w", noteDocumentID, err)
	}
	return nil
}

func (s *Store) SaveDocumentReadState(ctx context.Context, state port.DocumentReadState) error {
	if state.ProgressY <= 0 && strings.TrimSpace(state.ProgressAt) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM document_read_state WHERE document_id=?`, state.DocumentID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_read_state(document_id,progress_y,progress_at) VALUES(?,?,?)
ON CONFLICT(document_id) DO UPDATE SET progress_y=excluded.progress_y,progress_at=excluded.progress_at`,
		state.DocumentID, state.ProgressY, state.ProgressAt)
	if err != nil {
		return fmt.Errorf("saving read state for document %s: %w", state.DocumentID, err)
	}
	return nil
}

func (s *Store) GetDocumentReadState(ctx context.Context, documentID catalog.DocumentID) (port.DocumentReadState, bool, error) {
	state := port.DocumentReadState{DocumentID: documentID}
	err := s.db.QueryRowContext(ctx, `SELECT progress_y,progress_at FROM document_read_state WHERE document_id=?`, documentID).
		Scan(&state.ProgressY, &state.ProgressAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.DocumentReadState{}, false, nil
	}
	if err != nil {
		return port.DocumentReadState{}, false, fmt.Errorf("reading read state for document %s: %w", documentID, err)
	}
	return state, true, nil
}

func (s *Store) ListRecentDocuments(ctx context.Context, limit int) ([]port.RecentDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,COALESCE(i.title,''),l.relative_path,r.progress_at
FROM document_read_state r
JOIN documents d ON d.id=r.document_id
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
WHERE l.status='active' AND `+notTrashedClause+` AND trim(r.progress_at)!=''
ORDER BY r.progress_at DESC,d.id
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent documents: %w", err)
	}
	defer rows.Close()
	var documents []port.RecentDocument
	for rows.Next() {
		var document port.RecentDocument
		if err := rows.Scan(&document.DocumentID, &document.Title, &document.Path, &document.OpenedAt); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) ListRecentlyModifiedDocuments(ctx context.Context, limit int) ([]port.ModifiedDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,COALESCE(i.title,''),l.relative_path,
MAX(COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),
    COALESCE((SELECT MAX(MAX(an.updated_at,COALESCE(ni.source_updated_at,0)))
              FROM annotation_notes an
              LEFT JOIN document_index ni ON ni.document_id=an.note_document_id
              WHERE an.target_document_id=d.id
                AND an.note_document_id NOT IN (SELECT document_id FROM document_trash)),0)) AS modified_at
FROM documents d
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
WHERE l.status='active' AND `+notTrashedClause+`
ORDER BY modified_at DESC,d.id
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recently modified documents: %w", err)
	}
	defer rows.Close()
	var documents []port.ModifiedDocument
	for rows.Next() {
		var document port.ModifiedDocument
		var modifiedAt int64
		if err := rows.Scan(&document.DocumentID, &document.Title, &document.Path, &modifiedAt); err != nil {
			return nil, err
		}
		document.ModifiedAt = fromMillis(modifiedAt)
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

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
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents(id,created_at,updated_at,pinned) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at,pinned=excluded.pinned`, d.ID, millis(d.CreatedAt), millis(d.UpdatedAt), d.Pinned); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_index(document_id,title,summary,mtime,size,sha256,indexed_at,source_created_at,source_updated_at)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(document_id) DO UPDATE SET title=excluded.title,summary=excluded.summary,mtime=excluded.mtime,size=excluded.size,
sha256=excluded.sha256,indexed_at=excluded.indexed_at,source_created_at=excluded.source_created_at,source_updated_at=excluded.source_updated_at`,
		d.ID, d.Index.Title, d.Index.Summary, d.Index.MTime, d.Index.Size, d.Index.SHA256, millis(d.Index.IndexedAt), millis(d.Index.SourceCreatedAt), millis(d.Index.SourceUpdatedAt)); err != nil {
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
	// Persist clip provenance when front matter carries source_url (ingest + scan backfill).
	if sourceURL, clipMode := sourceMetaFromBody(string(save.Body), d.Location.RelativePath); sourceURL != "" {
		norm := normalizeSourceURLLite(sourceURL)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO document_sources(document_id, source_url, source_url_norm, clip_mode, created_at)
VALUES(?,?,?,?,?)
ON CONFLICT(document_id) DO UPDATE SET
  source_url=excluded.source_url,
  source_url_norm=excluded.source_url_norm,
  clip_mode=excluded.clip_mode
`, d.ID, norm, norm, clipMode, millis(d.UpdatedAt)); err != nil {
			return fmt.Errorf("saving document source: %w", err)
		}
	}
	return nil
}

func sourceMetaFromBody(body, relativePath string) (sourceURL, clipMode string) {
	if fm, ok := frontMatterMap(body); ok {
		sourceURL = strings.TrimSpace(fm["source_url"])
		clipMode = strings.TrimSpace(fm["clip_mode"])
	}
	if clipMode == "" && strings.HasSuffix(strings.ToLower(relativePath), "-note.md") {
		clipMode = "selection"
	}
	if clipMode == "" && sourceURL != "" {
		clipMode = "page"
	}
	return sourceURL, clipMode
}

func frontMatterMap(body string) (map[string]string, bool) {
	trim := strings.TrimSpace(body)
	if !strings.HasPrefix(trim, "---") {
		return nil, false
	}
	rest := trim[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		val := ""
		if len(parts) == 2 {
			val = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		}
		out[key] = val
	}
	return out, true
}

// normalizeSourceURLLite keeps store independent of the HTTP package while
// matching browser/weixin share-link variants to the same key.
func normalizeSourceURLLite(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Strip fragment.
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	// WeChat article id is stable; drop query entirely for mp.weixin.qq.com/s/…
	if strings.Contains(raw, "mp.weixin.qq.com/s/") || strings.Contains(raw, "weixin.qq.com/s/") {
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		return strings.TrimRight(raw, "/")
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		// Keep non-weixin queries only if essential; default strip for matching stability.
		raw = raw[:i]
	}
	return strings.TrimRight(raw, "/")
}

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
