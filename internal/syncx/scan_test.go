package syncx_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"membox/internal/application"
	"membox/internal/application/port"
	"membox/internal/domain/catalog"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
	"membox/internal/syncx"
)

// record builds a catalog record fixture. Relative paths are slash-style, as
// catalog.NewLocation normalizes them.
func record(t *testing.T, id string, status catalog.DocumentStatus, relPath, digest string, size, mtime int64) port.DocumentRecord {
	t.Helper()
	loc, err := catalog.NewLocation(1, relPath)
	if err != nil {
		t.Fatalf("bad location %q: %v", relPath, err)
	}
	return port.DocumentRecord{
		Document: &catalog.Document{
			ID:       catalog.DocumentID(id),
			Location: loc,
			Status:   status,
			Index:    catalog.IndexState{SHA256: digest, Size: size, MTime: mtime},
		},
	}
}

func TestEntryFromRecord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record port.DocumentRecord
		want   syncx.Entry
	}{
		{
			name:   "active document maps verbatim",
			record: record(t, "uuid-a", catalog.DocumentActive, "notes/deep/alpha.md", "deadbeef", 123, 456),
			want:   syncx.Entry{Exists: true, Name: "alpha.md", ContentSha256: "deadbeef", Size: 123, Mtime: 456},
		},
		{
			name:   "missing document is a tombstone keeping last-known fingerprint",
			record: record(t, "uuid-b", catalog.DocumentMissing, "gone.md", "deadbeef", 1, 1),
			want:   syncx.Entry{Exists: false, Name: "gone.md", ContentSha256: "deadbeef", Size: 1, Mtime: 1},
		},
		{
			name:   "untracked document is a tombstone keeping last-known fingerprint",
			record: record(t, "uuid-c", catalog.DocumentUntracked, "loose.md", "deadbeef", 1, 1),
			want:   syncx.Entry{Exists: false, Name: "loose.md", ContentSha256: "deadbeef", Size: 1, Mtime: 1},
		},
		{
			name:   "active document inside trash dir is a tombstone keeping last-known fingerprint",
			record: record(t, "uuid-d", catalog.DocumentActive, ".membox-trash/binned.md", "deadbeef", 1, 1),
			want:   syncx.Entry{Exists: false, Name: "binned.md", ContentSha256: "deadbeef", Size: 1, Mtime: 1},
		},
		{
			name:   "nil document yields zero entry",
			record: port.DocumentRecord{},
			want:   syncx.Entry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := syncx.EntryFromRecord(tt.record); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestScanCatalogKeysByUUIDAndSkipsNil(t *testing.T) {
	t.Parallel()
	docs := []port.DocumentRecord{
		record(t, "uuid-a", catalog.DocumentActive, "a.md", "sha-a", 1, 1),
		record(t, "uuid-b", catalog.DocumentMissing, "b.md", "sha-b", 2, 2),
		{}, // nil document: skipped
	}
	snap := syncx.ScanCatalog(docs)
	if len(snap) != 2 {
		t.Fatalf("snapshot size = %d, want 2", len(snap))
	}
	if !snap["uuid-a"].Exists {
		t.Fatal("uuid-a should exist")
	}
	if snap["uuid-b"].Exists {
		t.Fatal("uuid-b should be a tombstone")
	}
}

// TestScanCatalogIntegration proves the mapping against a real workspace:
// files on disk → membox scan → Service.ListDocuments → snapshot.
func TestScanCatalogIntegration(t *testing.T) {
	ctx := context.Background()
	home, notesDir := t.TempDir(), t.TempDir()

	alphaBody := []byte("# Alpha\n\nfirst note\n")
	betaBody := []byte("# Beta\n\nnested note\n")
	if err := os.WriteFile(filepath.Join(notesDir, "alpha.md"), alphaBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(notesDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "sub", "beta.md"), betaBody, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.Open(filepath.Join(home, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})

	// AddPath triggers the initial scan.
	if _, err := service.AddPath(ctx, notesDir); err != nil {
		t.Fatal(err)
	}
	docs, err := service.ListDocuments(ctx, 5000, false, "")
	if err != nil {
		t.Fatal(err)
	}

	snap := syncx.ScanCatalog(docs)
	if len(snap) != 2 {
		t.Fatalf("snapshot size = %d, want 2", len(snap))
	}

	byName := map[string]syncx.Entry{}
	for _, e := range snap {
		byName[e.Name] = e
	}
	for name, body := range map[string][]byte{"alpha.md": alphaBody, "beta.md": betaBody} {
		e, ok := byName[name]
		if !ok {
			t.Fatalf("%s missing from snapshot", name)
		}
		if !e.Exists {
			t.Fatalf("%s should exist", name)
		}
		sum := sha256.Sum256(body)
		if e.ContentSha256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s sha256 = %s, want %s", name, e.ContentSha256, hex.EncodeToString(sum[:]))
		}
		if e.Size != int64(len(body)) {
			t.Fatalf("%s size = %d, want %d", name, e.Size, len(body))
		}
	}
}
