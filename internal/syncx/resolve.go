package syncx

import (
	"errors"
	"fmt"
)

// ErrSidesDiffer means a --mark resolution was attempted while the two
// sides still disagree. Refusing protects the archive from recording a
// state that matches neither side — a forged history entry.
var ErrSidesDiffer = errors.New("syncx: sides still differ")

// ResolutionFrom settles a conflict by force: the winner side's state is
// imposed on the loser (bytes transfer as needed) and, once applied,
// becomes the new archive entry. Deleting-side wins are expressed with a
// tombstone winner.
func ResolutionFrom(uuid string, winner Entry, dir Direction) Change {
	return Change{UUID: uuid, Direction: dir, Winner: winner}
}

// ResolutionMark settles a conflict by hand: the user asserts both sides
// were manually made identical. The result applies through the same Change
// pipeline as ResolutionFrom (a no-op copy, since the sides already match),
// which records the agreed state in the archive. Mutual deletion is a valid
// resolution: both sides tombstoned marks the UUID as deleted for good.
func ResolutionMark(uuid string, alpha, beta Entry) (Change, error) {
	if !sameState(alpha, beta) {
		return Change{}, fmt.Errorf("%w: cannot mark %s resolved", ErrSidesDiffer, uuid)
	}
	return Change{UUID: uuid, Direction: AlphaToBeta, Winner: alpha}, nil
}
