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
); INSERT INTO documents(id,created_at,updated_at) VALUES('old-document',1,1)`); err != nil {
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
}
