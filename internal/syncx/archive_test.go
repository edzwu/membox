package syncx_test

import (
	"os"
	"path/filepath"
	"testing"

	"membox/internal/syncx"
)

func TestArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "archive.json")
	a := syncx.NewArchive()
	a.Entries["u1"] = syncx.Entry{Exists: true, Name: "a.md", ContentSha256: "sha-1", Size: 10, Mtime: 100}

	if err := syncx.SaveArchive(path, a); err != nil {
		t.Fatal(err)
	}
	loaded, err := syncx.LoadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != syncx.ArchiveVersion || len(loaded.Entries) != 1 || loaded.Entries["u1"] != a.Entries["u1"] {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
}

func TestLoadArchiveMissingFileYieldsEmpty(t *testing.T) {
	t.Parallel()
	a, err := syncx.LoadArchive(filepath.Join(t.TempDir(), "archive.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 0 {
		t.Fatalf("missing archive should load empty, got %+v", a)
	}
}

func TestLoadArchiveCorruptIsAnError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "archive.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncx.LoadArchive(path); err == nil {
		t.Fatal("corrupt archive must error, never silently reset shared history")
	}
}

func TestLoadArchiveWrongVersionIsAnError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "archive.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"entries":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncx.LoadArchive(path); err == nil {
		t.Fatal("unknown archive version must error")
	}
}

func TestArchiveApply(t *testing.T) {
	t.Parallel()
	a := syncx.NewArchive()
	a.Apply(syncx.Change{UUID: "u1", Winner: syncx.Entry{Exists: true, Name: "a.md", ContentSha256: "sha-1"}})
	if !a.Entries["u1"].Exists {
		t.Fatal("applied change should be recorded")
	}
	a.Apply(syncx.Change{UUID: "u1", Winner: syncx.Entry{Exists: false, Name: "a.md", ContentSha256: "sha-1"}})
	if _, ok := a.Entries["u1"]; ok {
		t.Fatal("propagated deletion should drop the archive entry")
	}
}
