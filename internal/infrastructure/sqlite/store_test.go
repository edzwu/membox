package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
	for _, table := range []string{"graph_edges"} {
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
