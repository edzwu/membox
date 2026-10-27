package syncx_test

import (
	"errors"
	"testing"

	"membox/internal/syncx"
)

// applyChange simulates the execution layer: the loser adopts the winner
// state and the archive records it.
func applyChange(archive *syncx.Archive, alpha, beta map[string]syncx.Entry, c syncx.Change) {
	loser := alpha
	if c.Direction == syncx.AlphaToBeta {
		loser = beta
	}
	if c.Winner.Exists {
		loser[c.UUID] = c.Winner
	} else {
		delete(loser, c.UUID)
	}
	archive.Apply(c)
}

func conflictedUUIDs(conflicts []syncx.Conflict) map[string]syncx.ConflictReason {
	m := map[string]syncx.ConflictReason{}
	for _, c := range conflicts {
		m[c.UUID] = c.Reason
	}
	return m
}

func TestResolutionFromConvergesNextRound(t *testing.T) {
	t.Parallel()
	base := doc("a.md", "sha-1")
	archive := syncx.NewArchive()
	archive.Entries["u1"] = base
	alpha := map[string]syncx.Entry{"u1": doc("a.md", "sha-2")}
	beta := map[string]syncx.Entry{"u1": doc("a.md", "sha-3")}

	// Round 1: conflict, nothing propagates.
	changes, conflicts := syncx.Reconcile(archive.Entries, alpha, beta)
	if len(changes) != 0 || len(conflicts) != 1 {
		t.Fatalf("round 1: changes=%d conflicts=%d", len(changes), len(conflicts))
	}

	// Resolve --from alpha, apply, next round must be silent.
	res := syncx.ResolutionFrom("u1", alpha["u1"], syncx.AlphaToBeta)
	applyChange(archive, alpha, beta, res)
	changes, conflicts = syncx.Reconcile(archive.Entries, alpha, beta)
	if len(changes) != 0 || len(conflicts) != 0 {
		t.Fatalf("after resolve: changes=%v conflicts=%v", changes, conflicts)
	}
	if beta["u1"].ContentSha256 != "sha-2" {
		t.Fatalf("loser did not adopt winner state: %+v", beta["u1"])
	}
}

func TestResolutionMarkConvergesNextRound(t *testing.T) {
	t.Parallel()
	base := doc("a.md", "sha-1")
	archive := syncx.NewArchive()
	archive.Entries["u1"] = base
	alpha := map[string]syncx.Entry{"u1": doc("a.md", "sha-2")}
	beta := map[string]syncx.Entry{"u1": doc("a.md", "sha-3")}

	// Mark while sides still differ: refused.
	if _, err := syncx.ResolutionMark("u1", alpha["u1"], beta["u1"]); !errors.Is(err, syncx.ErrSidesDiffer) {
		t.Fatalf("mark with differing sides: err = %v, want ErrSidesDiffer", err)
	}

	// User hand-merges beta to match alpha, then marks.
	beta["u1"] = alpha["u1"]
	res, err := syncx.ResolutionMark("u1", alpha["u1"], beta["u1"])
	if err != nil {
		t.Fatal(err)
	}
	applyChange(archive, alpha, beta, res)

	changes, conflicts := syncx.Reconcile(archive.Entries, alpha, beta)
	if len(changes) != 0 || len(conflicts) != 0 {
		t.Fatalf("after mark: changes=%v conflicts=%v", changes, conflicts)
	}
}

func TestResolutionDeleteVsModifyFromDeletingSide(t *testing.T) {
	t.Parallel()
	base := doc("a.md", "sha-1")
	archive := syncx.NewArchive()
	archive.Entries["u1"] = base
	alpha := map[string]syncx.Entry{} // deleted on alpha
	beta := map[string]syncx.Entry{"u1": doc("a.md", "sha-2")}

	_, conflicts := syncx.Reconcile(archive.Entries, alpha, beta)
	if got := conflictedUUIDs(conflicts)["u1"]; got != syncx.ConflictDeleteVsModify {
		t.Fatalf("reason = %q, want delete-vs-modify", got)
	}

	// --from alpha: the deletion wins; archive entry dropped for good.
	res := syncx.ResolutionFrom("u1", syncx.Entry{Exists: false, Name: "a.md"}, syncx.AlphaToBeta)
	applyChange(archive, alpha, beta, res)

	if _, ok := archive.Entries["u1"]; ok {
		t.Fatal("archive should drop the deleted entry")
	}
	if _, ok := beta["u1"]; ok {
		t.Fatal("beta should have deleted the document")
	}
	changes, conflicts := syncx.Reconcile(archive.Entries, alpha, beta)
	if len(changes) != 0 || len(conflicts) != 0 {
		t.Fatalf("after deletion resolve: changes=%v conflicts=%v", changes, conflicts)
	}
}

func TestResolutionMarkMutualDeletion(t *testing.T) {
	t.Parallel()
	// Both sides deleted while in conflict history: marking mutual deletion
	// drops the archive entry.
	archive := syncx.NewArchive()
	archive.Entries["u1"] = doc("a.md", "sha-1")
	res, err := syncx.ResolutionMark("u1", syncx.Entry{}, syncx.Entry{})
	if err != nil {
		t.Fatal(err)
	}
	applyChange(archive, map[string]syncx.Entry{}, map[string]syncx.Entry{}, res)
	if _, ok := archive.Entries["u1"]; ok {
		t.Fatal("mutual deletion should drop the archive entry")
	}
}
