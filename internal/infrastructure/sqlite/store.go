package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
    media_type TEXT NOT NULL DEFAULT 'text/markdown',
    metadata_overrides INTEGER NOT NULL DEFAULT 0,
    authors TEXT NOT NULL DEFAULT '',
    publication_year INTEGER NOT NULL DEFAULT 0,
    keywords TEXT NOT NULL DEFAULT '',
    page_count INTEGER NOT NULL DEFAULT 0,
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
    kind TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS annotation_notes_target ON annotation_notes(target_document_id);
CREATE TABLE IF NOT EXISTS card_schedule (
    note_document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    due_at INTEGER NOT NULL DEFAULT 0,
    interval_days INTEGER NOT NULL DEFAULT 0,
    ease REAL NOT NULL DEFAULT 2.5,
    reps INTEGER NOT NULL DEFAULT 0,
    lapses INTEGER NOT NULL DEFAULT 0,
    last_reviewed_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS document_read_state (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    progress_y INTEGER NOT NULL DEFAULT 0,
    progress_at TEXT NOT NULL DEFAULT ''
);
-- Canonical external URLs captured from inbox/knowledge documents. Resources
-- belong to membox; planning systems reference their stable IDs instead of
-- copying URL rows into task databases.
CREATE TABLE IF NOT EXISTS resources (
    id TEXT PRIMARY KEY,
    url TEXT NOT NULL,
    canonical_url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'M' CHECK(priority IN ('H','M','L')),
    score REAL CHECK(score IS NULL OR (score >= 0 AND score <= 1)),
    reason TEXT NOT NULL DEFAULT '',
    source_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    source_file TEXT NOT NULL DEFAULT '',
    source_line TEXT NOT NULL DEFAULT '',
    source_commit TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS resources_rank ON resources(priority, score DESC);
CREATE INDEX IF NOT EXISTS resources_created ON resources(created_at DESC);
CREATE TABLE IF NOT EXISTS resource_sources (
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    source_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    source_file TEXT NOT NULL DEFAULT '',
    source_line TEXT NOT NULL DEFAULT '',
    source_commit TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(resource_id, source_file, source_line)
);
CREATE INDEX IF NOT EXISTS resource_sources_document ON resource_sources(source_document_id);
CREATE INDEX IF NOT EXISTS resource_sources_file ON resource_sources(source_file);
CREATE TABLE IF NOT EXISTS resource_scan (
    source TEXT PRIMARY KEY,
    wave INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);
-- Logical content identity is independent of its physical representation.
-- V1 publishes whole-file direct objects; the schema can later add manifests
-- and chunks without changing document_versions.content_sha256.
CREATE TABLE IF NOT EXISTS contents (
    sha256 TEXT PRIMARY KEY,
    size INTEGER NOT NULL CHECK(size >= 0),
    representation TEXT NOT NULL CHECK(representation IN ('direct','chunked')),
    object_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS document_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    parent_version_id INTEGER REFERENCES document_versions(id),
    content_sha256 TEXT NOT NULL REFERENCES contents(sha256),
    reason TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS document_versions_document ON document_versions(document_id,id);
CREATE TABLE IF NOT EXISTS document_heads (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    version_id INTEGER NOT NULL REFERENCES document_versions(id) ON DELETE CASCADE
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
		{"media_type", `ALTER TABLE document_index ADD COLUMN media_type TEXT NOT NULL DEFAULT 'text/markdown'`},
		{"metadata_overrides", `ALTER TABLE document_index ADD COLUMN metadata_overrides INTEGER NOT NULL DEFAULT 0`},
		{"authors", `ALTER TABLE document_index ADD COLUMN authors TEXT NOT NULL DEFAULT ''`},
		{"publication_year", `ALTER TABLE document_index ADD COLUMN publication_year INTEGER NOT NULL DEFAULT 0`},
		{"keywords", `ALTER TABLE document_index ADD COLUMN keywords TEXT NOT NULL DEFAULT ''`},
		{"page_count", `ALTER TABLE document_index ADD COLUMN page_count INTEGER NOT NULL DEFAULT 0`},
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
	// Read state: semantic reading status (unread/reading/finished) beside
	// the existing scroll progress. Added in two steps so older DBs migrate.
	rsColumns := make(map[string]bool)
	rsRows, err := s.db.Query(`PRAGMA table_info(document_read_state)`)
	if err != nil {
		return fmt.Errorf("checking document_read_state columns: %w", err)
	}
	for rsRows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rsRows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			rsRows.Close()
			return fmt.Errorf("scanning table_info: %w", err)
		}
		rsColumns[name] = true
	}
	if err := rsRows.Close(); err != nil {
		return err
	}
	rsMigrations := []struct {
		name string
		sql  string
	}{
		{"read_status", `ALTER TABLE document_read_state ADD COLUMN read_status TEXT NOT NULL DEFAULT 'unread'`},
		{"finished_at", `ALTER TABLE document_read_state ADD COLUMN finished_at INTEGER`},
	}
	for _, migration := range rsMigrations {
		if rsColumns[migration.name] {
			continue
		}
		if _, err := s.db.Exec(migration.sql); err != nil {
			return fmt.Errorf("adding document_read_state.%s: %w", migration.name, err)
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
	if err := s.migrateAnnotationNoteKind(); err != nil {
		return err
	}
	if err := s.migrateQuestions(); err != nil {
		return err
	}
	return s.migrateAgentSessions()
}

// migrateQuestions creates the question accumulation table and adds inbox-scan
// provenance / canonical-dedupe columns used by /api/questions/ingest.
func (s *Store) migrateQuestions() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS questions (
    id TEXT PRIMARY KEY,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','answered','archived')),
    answer TEXT NOT NULL DEFAULT '',
    source_document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    answered_at INTEGER
);
CREATE INDEX IF NOT EXISTS questions_status ON questions(status, created_at DESC);
CREATE INDEX IF NOT EXISTS questions_source ON questions(source_document_id);
`)
	if err != nil {
		return fmt.Errorf("migrating questions: %w", err)
	}
	for _, column := range []struct{ name, def string }{
		{"canonical_body", "TEXT NOT NULL DEFAULT ''"},
		{"source_file", "TEXT NOT NULL DEFAULT ''"},
		{"source_line", "TEXT NOT NULL DEFAULT ''"},
		{"source_commit", "TEXT NOT NULL DEFAULT ''"},
	} {
		var has int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('questions') WHERE name=?`, column.name).Scan(&has)
		if err != nil {
			return fmt.Errorf("inspecting questions.%s: %w", column.name, err)
		}
		if has == 0 {
			if _, err := s.db.Exec(`ALTER TABLE questions ADD COLUMN ` + column.name + ` ` + column.def); err != nil {
				return fmt.Errorf("adding questions.%s: %w", column.name, err)
			}
		}
	}
	// Backfill canonical keys for rows created before inbox ingest existed.
	// Keep it SQL-simple (trim + lower); application-layer normalize is richer
	// for new writes and is what Ingest uses for dedupe going forward.
	if _, err := s.db.Exec(`UPDATE questions
SET canonical_body = lower(trim(body))
WHERE canonical_body = '' AND trim(body) != ''`); err != nil {
		return fmt.Errorf("backfilling questions.canonical_body: %w", err)
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS questions_canonical_body
ON questions(canonical_body) WHERE canonical_body != ''`); err != nil {
		return fmt.Errorf("indexing questions.canonical_body: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS questions_source_file
ON questions(source_file) WHERE source_file != ''`); err != nil {
		return fmt.Errorf("indexing questions.source_file: %w", err)
	}
	return nil
}

// migrateAnnotationNoteKind adds annotation_notes.kind so assist Q&A notes
// are distinguishable from plain selection notes at the database layer.
func (s *Store) migrateAnnotationNoteKind() error {
	var hasKind int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('annotation_notes') WHERE name='kind'`).Scan(&hasKind)
	if err != nil {
		return fmt.Errorf("inspecting annotation_notes.kind: %w", err)
	}
	if hasKind == 0 {
		if _, err := s.db.Exec(`ALTER TABLE annotation_notes ADD COLUMN kind TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("adding annotation_notes.kind: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS annotation_notes_kind ON annotation_notes(kind)`); err != nil {
		return fmt.Errorf("indexing annotation_notes.kind: %w", err)
	}
	return nil
}

func (s *Store) migrateAgentSessions() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS agent_sessions (
    id TEXT PRIMARY KEY,
    pi_session_id TEXT UNIQUE,
    session_path TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    model_provider TEXT,
    model_id TEXT,
    thinking_level TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS agent_sessions_last_used ON agent_sessions(last_used_at DESC);
CREATE INDEX IF NOT EXISTS agent_sessions_archived ON agent_sessions(archived);
`)
	if err != nil {
		return fmt.Errorf("migrating agent_sessions: %w", err)
	}
	return nil
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
