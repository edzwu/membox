package syncx_test

import (
	"os"
	"path/filepath"
	"testing"

	"membox/internal/syncx"
)

func conflictFixture() []syncx.Conflict {
	return []syncx.Conflict{
		{UUID: "u1", Reason: syncx.ConflictBothModified, Alpha: doc("a.md", "sha-2"), Beta: doc("a.md", "sha-3")},
		{UUID: "u2", Reason: syncx.ConflictDeleteVsModify, Alpha: syncx.Entry{}, Beta: doc("b.md", "sha-9")},
	}
}

func TestConflictsRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "conflicts.json")
	want := conflictFixture()
	if err := syncx.SaveConflicts(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := syncx.LoadConflicts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestLoadConflictsMissingFileYieldsEmpty(t *testing.T) {
	t.Parallel()
	got, err := syncx.LoadConflicts(filepath.Join(t.TempDir(), "conflicts.json"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestLoadConflictsCorruptIsAnError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "conflicts.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncx.LoadConflicts(path); err == nil {
		t.Fatal("corrupt conflict list must error")
	}
}

func TestRemoveConflict(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "conflicts.json")
	if err := syncx.SaveConflicts(path, conflictFixture()); err != nil {
		t.Fatal(err)
	}
	if err := syncx.RemoveConflict(path, "u1"); err != nil {
		t.Fatal(err)
	}
	got, err := syncx.LoadConflicts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UUID != "u2" {
		t.Fatalf("after remove: %+v", got)
	}
	// Removing an absent uuid is a no-op, not an error.
	if err := syncx.RemoveConflict(path, "u404"); err != nil {
		t.Fatal(err)
	}
}
