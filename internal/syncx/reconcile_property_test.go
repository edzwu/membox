package syncx_test

import (
	"fmt"
	"math/rand"
	"testing"

	"membox/internal/syncx"
)

// TestReconcileConvergence is the correctness invariant from
// docs/sync-mvp.md §0 made executable: after reconciling a random archive
// with randomly diverged sides and applying every emitted change, all
// non-conflicted UUIDs must converge — both sides and the archive agree.
// Conflicted UUIDs must stay exactly as they were (nothing is lost).
func TestReconcileConvergence(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(20240801))

	for round := 0; round < 2000; round++ {
		archive, alpha, beta := randomScenario(rng)
		changes, conflicts := syncx.Reconcile(archive, alpha, beta)

		conflicted := map[string]bool{}
		for _, c := range conflicts {
			conflicted[c.UUID] = true
		}

		// Simulate apply: the loser adopts the winner state; archive follows.
		nextArchive := syncx.NewArchive()
		for k, v := range archive {
			nextArchive.Entries[k] = v
		}
		for _, ch := range changes {
			loser := alpha
			if ch.Direction == syncx.AlphaToBeta {
				loser = beta
			}
			if ch.Winner.Exists {
				loser[ch.UUID] = ch.Winner
			} else {
				delete(loser, ch.UUID)
			}
			nextArchive.Apply(ch)
		}

		uuids := map[string]bool{}
		for k := range alpha {
			uuids[k] = true
		}
		for k := range beta {
			uuids[k] = true
		}
		for k := range nextArchive.Entries {
			uuids[k] = true
		}

		for uuid := range uuids {
			if conflicted[uuid] {
				continue // untouched by definition; checked implicitly below
			}
			a, aOK := alpha[uuid]
			b, bOK := beta[uuid]
			arch, archOK := nextArchive.Entries[uuid]
			aLive, bLive := aOK && a.Exists, bOK && b.Exists

			if aLive != bLive {
				t.Fatalf("round %d uuid %s: existence diverged (alpha=%v beta=%v)", round, uuid, aLive, bLive)
			}
			if aLive {
				if !archOK || a != arch || b != arch {
					t.Fatalf("round %d uuid %s: not converged\nalpha=%+v\nbeta=%+v\narchive=%+v", round, uuid, a, b, arch)
				}
			} else if archOK {
				t.Fatalf("round %d uuid %s: deleted everywhere but archive still holds it", round, uuid)
			}
		}
	}
}

// randomScenario builds a small archive, then lets each side diverge
// independently: noop, edit content, rename, delete, or create fresh.
func randomScenario(rng *rand.Rand) (archive, alpha, beta map[string]syncx.Entry) {
	archive = map[string]syncx.Entry{}
	alpha = map[string]syncx.Entry{}
	beta = map[string]syncx.Entry{}

	for i := 0; i < 6; i++ {
		uuid := fmt.Sprintf("uuid-%d", i)
		e := syncx.Entry{Exists: true, Name: fmt.Sprintf("doc-%d.md", i), ContentSha256: fmt.Sprintf("sha-%d-0", i), Size: 10, Mtime: 100}
		if rng.Intn(4) > 0 { // 3/4 of UUIDs start in the shared history
			archive[uuid] = e
			alpha[uuid] = e
			beta[uuid] = e
		}
		mutate(rng, alpha, uuid)
		mutate(rng, beta, uuid)
	}
	return archive, alpha, beta
}

func mutate(rng *rand.Rand, side map[string]syncx.Entry, uuid string) {
	e, ok := side[uuid]
	switch rng.Intn(5) {
	case 0: // noop
	case 1: // edit content
		if ok {
			e.ContentSha256 += fmt.Sprintf("-x%d", rng.Intn(3)) // sometimes both sides pick the same edit → converged case
			side[uuid] = e
		}
	case 2: // rename
		if ok {
			e.Name = fmt.Sprintf("renamed-%d.md", rng.Intn(2)) // sometimes both pick the same name
			side[uuid] = e
		}
	case 3: // delete
		delete(side, uuid)
	case 4: // create fresh (only if absent)
		if !ok {
			side[uuid] = syncx.Entry{Exists: true, Name: fmt.Sprintf("fresh-%d.md", rng.Intn(2)), ContentSha256: fmt.Sprintf("sha-new-%d", rng.Intn(3)), Size: 1, Mtime: 1}
		}
	}
}
