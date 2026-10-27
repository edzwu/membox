package syncx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ConflictListVersion is the on-disk schema version of the conflict list.
const ConflictListVersion = 1

// conflictList is the persisted form of the unresolved-conflict queue:
// the user's sync todo list, written by `sync run`, read by `sync status`.
type conflictList struct {
	Version   int        `json:"version"`
	Conflicts []Conflict `json:"conflicts"`
}

// SaveConflicts writes the unresolved-conflict queue atomically.
func SaveConflicts(path string, conflicts []Conflict) error {
	if conflicts == nil {
		conflicts = []Conflict{}
	}
	return writeJSONAtomic(path, conflictList{Version: ConflictListVersion, Conflicts: conflicts})
}

// LoadConflicts reads the queue. A missing file yields an empty queue; a
// corrupt or wrong-version file is an error.
func LoadConflicts(path string) ([]Conflict, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l conflictList
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("corrupt conflict list %s: %w", path, err)
	}
	if l.Version != ConflictListVersion {
		return nil, fmt.Errorf("conflict list %s: version %d, want %d", path, l.Version, ConflictListVersion)
	}
	return l.Conflicts, nil
}

// RemoveConflict drops uuid from the queue file (after a successful
// resolve). Absence is not an error.
func RemoveConflict(path string, uuid string) error {
	conflicts, err := LoadConflicts(path)
	if err != nil {
		return err
	}
	kept := conflicts[:0]
	for _, c := range conflicts {
		if c.UUID != uuid {
			kept = append(kept, c)
		}
	}
	if len(kept) == len(conflicts) {
		return nil
	}
	return SaveConflicts(path, kept)
}
