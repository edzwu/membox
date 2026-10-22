package syncx_test

import (
	"testing"

	"membox/internal/syncx"
)

func doc(name, digest string) syncx.Entry {
	return syncx.Entry{Exists: true, Name: name, ContentSha256: digest, Size: 10, Mtime: 100}
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	base := doc("a.md", "sha-1")

	tests := []struct {
		name          string
		archive       map[string]syncx.Entry
		alpha, beta   map[string]syncx.Entry
		wantChanges   []syncx.Change
		wantConflicts []syncx.Conflict
	}{
		{
			name:    "unchanged everywhere is a no-op",
			archive: map[string]syncx.Entry{"u1": base},
			alpha:   map[string]syncx.Entry{"u1": base},
			beta:    map[string]syncx.Entry{"u1": base},
		},
		{
			name:        "alpha content edit propagates to beta",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			beta:        map[string]syncx.Entry{"u1": base},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: doc("a.md", "sha-2")}},
		},
		{
			name:        "beta content edit propagates to alpha",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{"u1": base},
			beta:        map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.BetaToAlpha, Winner: doc("a.md", "sha-2")}},
		},
		{
			name:        "rename is a content-level change and propagates",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{"u1": doc("renamed.md", "sha-1")},
			beta:        map[string]syncx.Entry{"u1": base},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: doc("renamed.md", "sha-1")}},
		},
		{
			name:        "new document on alpha is created on beta",
			alpha:       map[string]syncx.Entry{"u1": base},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: base}},
		},
		{
			name:        "new document on beta is created on alpha",
			beta:        map[string]syncx.Entry{"u1": base},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.BetaToAlpha, Winner: base}},
		},
		{
			name:        "alpha deletion propagates to beta as tombstone keeping the last-known fingerprint",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{}, // deleted
			beta:        map[string]syncx.Entry{"u1": base},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: syncx.Entry{Exists: false, Name: "a.md", ContentSha256: "sha-1", Size: 10, Mtime: 100}}},
		},
		{
			name:        "deletion on both sides emits an idempotent tombstone so the archive drops the entry",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{},
			beta:        map[string]syncx.Entry{},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: syncx.Entry{Exists: false, Name: "a.md", ContentSha256: "sha-1", Size: 10, Mtime: 100}}},
		},
		{
			name:          "both sides edited content: conflict, nothing propagates",
			archive:       map[string]syncx.Entry{"u1": base},
			alpha:         map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			beta:          map[string]syncx.Entry{"u1": doc("a.md", "sha-3")},
			wantConflicts: []syncx.Conflict{{UUID: "u1", Reason: syncx.ConflictBothModified, Alpha: doc("a.md", "sha-2"), Beta: doc("a.md", "sha-3")}},
		},
		{
			name:          "rename versus edit is a conflict (conservative rule)",
			archive:       map[string]syncx.Entry{"u1": base},
			alpha:         map[string]syncx.Entry{"u1": doc("renamed.md", "sha-1")},
			beta:          map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			wantConflicts: []syncx.Conflict{{UUID: "u1", Reason: syncx.ConflictBothModified, Alpha: doc("renamed.md", "sha-1"), Beta: doc("a.md", "sha-2")}},
		},
		{
			name:          "delete versus modify never silently deletes fresh edits",
			archive:       map[string]syncx.Entry{"u1": base},
			alpha:         map[string]syncx.Entry{}, // deleted
			beta:          map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			wantConflicts: []syncx.Conflict{{UUID: "u1", Reason: syncx.ConflictDeleteVsModify, Alpha: syncx.Entry{}, Beta: doc("a.md", "sha-2")}},
		},
		{
			name:        "identical independent edits converge without a conflict",
			archive:     map[string]syncx.Entry{"u1": base},
			alpha:       map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			beta:        map[string]syncx.Entry{"u1": doc("a.md", "sha-2")},
			wantChanges: []syncx.Change{{UUID: "u1", Direction: syncx.AlphaToBeta, Winner: doc("a.md", "sha-2")}},
		},
		{
			name:          "same UUID created independently with divergent content is a both-created conflict",
			alpha:         map[string]syncx.Entry{"u1": doc("a.md", "sha-1")},
			beta:          map[string]syncx.Entry{"u1": doc("a.md", "sha-9")},
			wantConflicts: []syncx.Conflict{{UUID: "u1", Reason: syncx.ConflictBothCreated, Alpha: doc("a.md", "sha-1"), Beta: doc("a.md", "sha-9")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			changes, conflicts := syncx.Reconcile(tt.archive, tt.alpha, tt.beta)
			if !equalChanges(changes, tt.wantChanges) {
				t.Fatalf("changes = %+v, want %+v", changes, tt.wantChanges)
			}
			if !equalConflicts(conflicts, tt.wantConflicts) {
				t.Fatalf("conflicts = %+v, want %+v", conflicts, tt.wantConflicts)
			}
		})
	}
}

func equalChanges(a, b []syncx.Change) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalConflicts(a, b []syncx.Conflict) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
