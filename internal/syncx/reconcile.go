package syncx

import "sort"

// Direction says which side's state propagates to the other.
type Direction int

const (
	AlphaToBeta Direction = iota
	BetaToAlpha
)

func (d Direction) String() string {
	if d == AlphaToBeta {
		return "alpha→beta"
	}
	return "beta→alpha"
}

// Change is a reconcile verdict: copy Winner's state to the losing side.
// Winner.Exists == false means propagate a deletion (the receiving side
// trashes its copy). Byte transfer follows only when the loser actually
// needs the blob (see the BlobStore layer).
type Change struct {
	UUID      string
	Direction Direction
	Winner    Entry
}

// ConflictReason classifies why a UUID could not be reconciled.
type ConflictReason string

const (
	// ConflictBothModified: both sides changed content and/or name relative
	// to the archive (rename-vs-edit counts; the design is conservative).
	ConflictBothModified ConflictReason = "both-modified"
	// ConflictDeleteVsModify: one side deleted, the other modified. Never
	// silently delete fresh edits.
	ConflictDeleteVsModify ConflictReason = "delete-vs-modify"
	// ConflictBothCreated: both sides created the same UUID independently
	// with divergent content. Essentially unreachable with UUIDv7 (semantic
	// collision only), but handled so the invariant never depends on luck.
	ConflictBothCreated ConflictReason = "both-created"
)

// Conflict is a UUID reconcile refused to touch. Both sides and the archive
// keep their state; the conflict resurfaces every round until resolved.
type Conflict struct {
	UUID        string
	Reason      ConflictReason
	Alpha, Beta Entry
}

// sameState compares the reconcile-relevant fields. Size/Mtime are scan
// fast-path hints only and play no role here.
func sameState(a, b Entry) bool {
	return a.Exists == b.Exists && a.Name == b.Name && a.ContentSha256 == b.ContentSha256
}

// tombstone builds the deletion state propagated to the losing side, keeping
// the archive's last-known fingerprint for future delete-vs-modify checks.
func tombstone(arch Entry) Entry {
	arch.Exists = false
	return arch
}

// Reconcile is the brain of the sync engine: a pure function from three
// metadata snapshots (archive, α, β) to verdicts. No IO, no clock, no
// randomness — same input, same output. Output order is deterministic
// (sorted by UUID).
//
// Rules (docs/sync-mvp.md §3):
//   - one side changed, other unchanged → propagate the change
//   - both changed (name or content) → conflict, skip
//   - both changed identically → already converged; emit a no-op change so
//     the archive can catch up
//   - one side deleted, other unchanged → propagate the deletion
//   - one side deleted, other modified → conflict
//   - UUID unknown to the archive and present on exactly one side → create
func Reconcile(archive, alpha, beta map[string]Entry) ([]Change, []Conflict) {
	uuids := unionKeys(archive, alpha, beta)
	var changes []Change
	var conflicts []Conflict

	for _, uuid := range uuids {
		a, b := alpha[uuid], beta[uuid] // zero Entry when the side never saw it
		arch, known := archive[uuid]
		known = known && arch.Exists

		switch {
		case a.Exists && b.Exists:
			aChanged := !known || !sameState(a, arch)
			bChanged := !known || !sameState(b, arch)
			switch {
			case !aChanged && !bChanged:
				// nothing moved
			case aChanged && !bChanged:
				changes = append(changes, Change{uuid, AlphaToBeta, a})
			case bChanged && !aChanged:
				changes = append(changes, Change{uuid, BetaToAlpha, b})
			case sameState(a, b):
				// Both sides made the same change independently; nothing to
				// copy, but emit an idempotent change so the archive catches up.
				changes = append(changes, Change{uuid, AlphaToBeta, a})
			default:
				reason := ConflictBothModified
				if !known {
					reason = ConflictBothCreated
				}
				conflicts = append(conflicts, Conflict{uuid, reason, a, b})
			}

		case a.Exists && !b.Exists:
			// β lost it. Was it β deleting, or α creating after the archive?
			if !known {
				changes = append(changes, Change{uuid, AlphaToBeta, a}) // α created
			} else if sameState(a, arch) {
				changes = append(changes, Change{uuid, BetaToAlpha, tombstone(arch)}) // β deleted
			} else {
				conflicts = append(conflicts, Conflict{uuid, ConflictDeleteVsModify, a, b})
			}

		case !a.Exists && b.Exists:
			if !known {
				changes = append(changes, Change{uuid, BetaToAlpha, b}) // β created
			} else if sameState(b, arch) {
				changes = append(changes, Change{uuid, AlphaToBeta, tombstone(arch)}) // α deleted
			} else {
				conflicts = append(conflicts, Conflict{uuid, ConflictDeleteVsModify, a, b})
			}

		default:
			// Absent on both sides: fully deleted. Emit an idempotent
			// tombstone change so Archive.Apply drops the entry instead of
			// leaking it forever.
			if known {
				changes = append(changes, Change{uuid, AlphaToBeta, tombstone(arch)})
			}
		}
	}
	return changes, conflicts
}

func unionKeys(maps ...map[string]Entry) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
