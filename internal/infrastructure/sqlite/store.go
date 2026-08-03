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
	return s.migrateGraphSchema()
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
		_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS graph_edges_from ON graph_edges(from_document_id);
CREATE INDEX IF NOT EXISTS graph_edges_to ON graph_edges(to_document_id)`)
		return err
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
	return tx.Commit()
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

const documentSelect = `SELECT d.id,d.created_at,d.updated_at,d.pinned,l.path_id,l.relative_path,l.file_key,l.status,
COALESCE(i.title,''),COALESCE(i.summary,''),COALESCE(i.mtime,0),COALESCE(i.size,0),COALESCE(i.sha256,''),i.indexed_at,
COALESCE(i.source_created_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.created_at END),
COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),p.root_path
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

// SaveAnnotations upserts the Miru annotation sidecar for a document. An
// empty sidecar clears any stored annotations.
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

// GetAnnotations returns the stored annotation sidecar, or "" if none.
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

func (s *Store) ResolveTopic(ctx context.Context, selector string) (*catalog.Document, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, "", errors.New("topic selector is required")
	}
	records, err := s.topicDocumentRecords(ctx, ` WHERE l.relative_path LIKE 'topic-%.md' AND l.status != 'untracked' AND (d.id=? OR lower(l.relative_path)=lower(?) OR lower(COALESCE(i.title,''))=lower(?) OR lower(REPLACE(COALESCE(i.title,''),' ','-'))=lower(?))`, selector, "topic-"+topicFileSlug(selector)+".md", selector, selector)
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
	return s.topicDocumentRecords(ctx, ` WHERE l.relative_path LIKE 'topic-%.md' AND l.status != 'untracked'`)
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
	records := make([]port.DocumentRecord, 0, len(ids))
	for _, id := range ids {
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
