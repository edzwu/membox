package syncx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ArchiveVersion is the on-disk schema version of the sync archive.
const ArchiveVersion = 1

// Archive is the sync engine's only memory: the document state both sides
// agreed on after the last successful sync round (think git merge base).
// It shares the Entry shape with snapshots — same type, different role:
// snapshots are observed and volatile, the archive is recorded and
// persistent.
type Archive struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"` // UUID → last agreed state
}

// NewArchive returns an empty archive for a first sync.
func NewArchive() *Archive {
	return &Archive{Version: ArchiveVersion, Entries: map[string]Entry{}}
}

// LoadArchive reads the archive from path. A missing file is not an error —
// it yields an empty archive (first sync). A corrupt or wrong-version file
// IS an error: shared history must never be silently reset, because an empty
// archive makes every document look newly created on both sides.
func LoadArchive(path string) (*Archive, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewArchive(), nil
	}
	if err != nil {
		return nil, err
	}
	var a Archive
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("corrupt archive %s: %w", path, err)
	}
	if a.Version != ArchiveVersion {
		return nil, fmt.Errorf("archive %s: version %d, want %d", path, a.Version, ArchiveVersion)
	}
	if a.Entries == nil {
		a.Entries = map[string]Entry{}
	}
	return &a, nil
}

// writeJSONAtomic writes v as JSON atomically: temp file in the same
// directory, fsync, then rename. A crash mid-write leaves the previous
// file intact — losing history is worse than losing progress.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// SaveArchive writes the archive atomically.
func SaveArchive(path string, a *Archive) error {
	return writeJSONAtomic(path, a)
}

// Apply records one successfully propagated change: the winner state becomes
// the new agreed state; deletions drop the entry. Conflicts are never
// applied, so their old archive entry survives and the conflict resurfaces
// next round instead of being silently swallowed.
func (a *Archive) Apply(c Change) {
	if a.Entries == nil {
		a.Entries = map[string]Entry{}
	}
	if c.Winner.Exists {
		a.Entries[c.UUID] = c.Winner
	} else {
		delete(a.Entries, c.UUID)
	}
}
