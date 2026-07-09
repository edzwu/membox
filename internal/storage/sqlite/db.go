// Package sqlite provides a SQLite connection helper.
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const defaultDBPath = "data/membox.sqlite"

// noteSchema is the DDL required for note metadata and their FTS5 title index.
// Markdown content lives on the filesystem; SQLite only tracks metadata.
//
// Connection-level pragmas (foreign_keys, journal_mode, busy_timeout) are set
// in the DSN in Open() rather than here, so they are applied consistently for
// every connection in the pool.
const noteSchema = `
CREATE TABLE IF NOT EXISTS note (
  uuid TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  tags TEXT NOT NULL DEFAULT '[]',  -- JSON array of tags
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS note_fts USING fts5(
  title,
  content='note',
  content_rowid='rowid',
  tokenize='unicode61'
);

CREATE TRIGGER IF NOT EXISTS trg_note_ai
AFTER INSERT ON note
BEGIN
  INSERT INTO note_fts(rowid, title)
  VALUES (new.rowid, new.title);
END;

CREATE TRIGGER IF NOT EXISTS trg_note_ad
AFTER DELETE ON note
BEGIN
  INSERT INTO note_fts(note_fts, rowid, title)
  VALUES ('delete', old.rowid, old.title);
END;

CREATE TRIGGER IF NOT EXISTS trg_note_au
AFTER UPDATE ON note
WHEN old.title != new.title
BEGIN
  INSERT INTO note_fts(note_fts, rowid, title)
  VALUES ('delete', old.rowid, old.title);
  INSERT INTO note_fts(rowid, title)
  VALUES (new.rowid, new.title);
END;

-- Perkeep sync state: maps local note UUID to remote permanode blobref.
CREATE TABLE IF NOT EXISTS perkeep_sync (
  uuid TEXT PRIMARY KEY,
  permanode TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  synced_at TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (uuid) REFERENCES note(uuid) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_perkeep_sync_permanode ON perkeep_sync(permanode);
`

// Open returns a *sql.DB for the given path, creating the file if necessary.
// An empty dbPath falls back to MEMBOX_DB_PATH, then data/membox.sqlite.
// DB wraps *sql.DB so callers can close the underlying connection pool and
// access it for repository construction.
type DB struct {
	db *sql.DB
}

// DB returns the underlying *sql.DB.
func (d *DB) DB() *sql.DB {
	return d.db
}

// Close closes the underlying database connection pool.
func (d *DB) Close() error {
	return d.db.Close()
}

// Open returns a DB for the given path, creating the file if necessary.
// An empty dbPath falls back to MEMBOX_DB_PATH, then data/membox.sqlite.
func Open(dbPath string) (*DB, error) {
	if dbPath == "" {
		dbPath = defaultDBPath
		if v := os.Getenv("MEMBOX_DB_PATH"); v != "" {
			dbPath = v
		}
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path %q: %w", dbPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", absPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", absPath, err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite %q: %w", absPath, err)
	}
	if _, err := db.Exec(noteSchema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &DB{db: db}, nil
}
