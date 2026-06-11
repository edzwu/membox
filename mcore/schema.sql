PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS doc (
  doc_id TEXT PRIMARY KEY,
  source_path TEXT NOT NULL,
  title TEXT,
  mime TEXT,
  sha256 TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_doc_source_path ON doc(source_path);

CREATE TABLE IF NOT EXISTS chunk (
  chunk_id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES doc(doc_id) ON DELETE CASCADE,
  chunk_index INTEGER NOT NULL,
  page_start INTEGER,
  page_end INTEGER,
  text TEXT NOT NULL,
  char_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunk_doc ON chunk(doc_id, chunk_index);

CREATE VIRTUAL TABLE IF NOT EXISTS chunk_fts USING fts5(
  text,
  doc_id UNINDEXED,
  chunk_id UNINDEXED,
  page_start UNINDEXED,
  tokenize = 'unicode61'
);

CREATE TRIGGER IF NOT EXISTS trg_chunk_ai AFTER INSERT ON chunk BEGIN
  INSERT INTO chunk_fts(rowid, text, doc_id, chunk_id, page_start)
  VALUES (new.rowid, new.text, new.doc_id, new.chunk_id, new.page_start);
END;

CREATE TRIGGER IF NOT EXISTS trg_chunk_ad AFTER DELETE ON chunk BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, text, doc_id, chunk_id, page_start)
  VALUES ('delete', old.rowid, old.text, old.doc_id, old.chunk_id, old.page_start);
END;

CREATE TRIGGER IF NOT EXISTS trg_chunk_au AFTER UPDATE ON chunk BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, text, doc_id, chunk_id, page_start)
  VALUES ('delete', old.rowid, old.text, old.doc_id, old.chunk_id, old.page_start);
  INSERT INTO chunk_fts(rowid, text, doc_id, chunk_id, page_start)
  VALUES (new.rowid, new.text, new.doc_id, new.chunk_id, new.page_start);
END;

CREATE TABLE IF NOT EXISTS embedding (
  chunk_id TEXT PRIMARY KEY REFERENCES chunk(chunk_id) ON DELETE CASCADE,
  embedding_model TEXT NOT NULL,
  embedding_dim INTEGER NOT NULL,
  vec BLOB NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_embedding_model ON embedding(embedding_model);

-- Generic note ingest (aligned with mvp.md Hub documents concept)
CREATE TABLE IF NOT EXISTS note (
  uuid TEXT PRIMARY KEY,
  backend TEXT,
  subdir TEXT,
  source_url TEXT,
  title TEXT,
  tags TEXT, -- JSON array
  content TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS note_fts USING fts5(
  title, content,
  content='note',
  content_rowid='rowid',
  tokenize='porter'
);

CREATE TRIGGER IF NOT EXISTS trg_note_ai AFTER INSERT ON note BEGIN
  INSERT INTO note_fts(rowid, title, content)
  VALUES (new.rowid, new.title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS trg_note_ad AFTER DELETE ON note BEGIN
  INSERT INTO note_fts(note_fts, rowid, title, content)
  VALUES ('delete', old.rowid, old.title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS trg_note_au AFTER UPDATE ON note BEGIN
  INSERT INTO note_fts(note_fts, rowid, title, content)
  VALUES ('delete', old.rowid, old.title, old.content);
  INSERT INTO note_fts(rowid, title, content)
  VALUES (new.rowid, new.title, new.content);
END;
