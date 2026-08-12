package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestListRecentlyModifiedDocumentsSortsSourceAndAnnotationChanges(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.db.Exec(`
INSERT INTO paths(id,root_path,created_at,status,last_error) VALUES(1,'/notes',1,'ready','');
INSERT INTO documents(id,created_at,updated_at,pinned) VALUES
  ('doc-a',1,1,0),('doc-b',1,1,0),('note-c',1,1,0);
INSERT INTO document_locations(document_id,path_id,relative_path,file_key,status,last_seen_at) VALUES
  ('doc-a',1,'a.md','a','active',1),
  ('doc-b',1,'b.md','b','active',1),
  ('note-c',1,'a-note.md','c','active',1);
INSERT INTO document_index(document_id,title,mtime,size,sha256,indexed_at,source_created_at,source_updated_at) VALUES
  ('doc-a','A',0,0,'',1,1,100),
  ('doc-b','B',0,0,'',1,1,200),
  ('note-c','C',0,0,'',1,1,50);
INSERT INTO annotation_notes(note_document_id,target_document_id,anchor_start,anchor_prefix,anchor_suffix,
  highlight,underline,strikethrough,created_at,updated_at)
VALUES('note-c','doc-a',0,'','',1,0,0,1,300);`); err != nil {
		t.Fatal(err)
	}

	documents, err := store.ListRecentlyModifiedDocuments(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 3 || documents[0].DocumentID != "doc-a" || documents[1].DocumentID != "doc-b" || documents[2].DocumentID != "note-c" {
		t.Fatalf("documents not sorted by effective modification: %+v", documents)
	}
	if documents[0].ModifiedAt.UnixMilli() != 300 || documents[1].ModifiedAt.UnixMilli() != 200 {
		t.Fatalf("unexpected modification times: %+v", documents)
	}
}

func TestOpenMigratesPinnedColumnForExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "membox.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE documents (
id TEXT PRIMARY KEY,
created_at INTEGER NOT NULL,
updated_at INTEGER NOT NULL
); INSERT INTO documents(id,created_at,updated_at) VALUES('old-document',1,1);
CREATE TABLE graph_nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE graph_edges (
    from_node_id TEXT NOT NULL,
    to_node_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (from_node_id, to_node_id, kind)
);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var pinned int
	if err := store.db.QueryRow(`SELECT pinned FROM documents WHERE id='old-document'`).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned != 0 {
		t.Fatalf("migrated pin default=%d, want 0", pinned)
	}
	for _, table := range []string{"graph_edges", "annotation_notes", "document_read_state", "contents", "document_versions", "document_heads"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("migration did not create %s", table)
		}
	}
	var graphNodes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='graph_nodes'`).Scan(&graphNodes); err != nil {
		t.Fatal(err)
	}
	if graphNodes != 0 {
		t.Fatal("legacy graph_nodes table still exists")
	}
}

func TestSearchExactSkipsPrefixMatches(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`
INSERT INTO paths(id,root_path,created_at,status,last_error) VALUES(1,'/notes',1,'ready','');
INSERT INTO documents(id,created_at,updated_at,pinned) VALUES('wal-doc',1,1,0),('wall-doc',1,1,0);
INSERT INTO document_locations(document_id,path_id,relative_path,file_key,status,last_seen_at) VALUES
  ('wal-doc',1,'wal.md','w','active',1),('wall-doc',1,'wall.md','wl','active',1);
INSERT INTO document_index(document_id,title,mtime,size,sha256,indexed_at,source_created_at,source_updated_at) VALUES
  ('wal-doc','Wal',0,0,'',1,1,1),('wall-doc','Wall',0,0,'',1,1,1);
INSERT INTO document_fts(document_id,title,path,body) VALUES
  ('wal-doc','Wal','/notes/wal.md','wal'),
  ('wall-doc','Wall','/notes/wall.md','wall');`); err != nil {
		t.Fatal(err)
	}

	prefix, err := store.Search(context.Background(), "wal", 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix) != 2 {
		t.Fatalf("prefix search should hit wal AND wall: %+v", prefix)
	}
	exact, err := store.Search(context.Background(), "wal", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact) != 1 || exact[0].DocumentID != "wal-doc" {
		t.Fatalf("exact search should hit only wal: %+v", exact)
	}
}
