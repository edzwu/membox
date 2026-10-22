// Package syncx implements the metadata layer of membox's sync engine.
// Design rule (docs/sync-mvp.md): metadata replicates fully, blobs lazily.
// This package is a pure library — no ctx, no IO, no storage. Callers own
// data fetching; syncx only maps records and (in later milestones)
// reconciles snapshots.
package syncx

// Entry is the complete per-document sync state. Identity is the snapshot's
// map key (the physical document UUID); everything reconcile needs is here.
type Entry struct {
	// Exists is false for tombstones (missing, untracked, or trashed
	// documents). Tombstones propagate deletion to the other side and
	// retain the last-known fingerprint so reconcile can see what was
	// deleted.
	Exists bool `json:"exists"`
	// Name is the basename only. Directories are workspace-local
	// implementation detail and never sync. A rename is a content-level
	// change of this field.
	Name string `json:"name"`
	// ContentSha256 is the blob-layer key: bytes are content-addressed and
	// verified against this digest on delivery.
	ContentSha256 string `json:"sha256"`
	// Size and Mtime are the fast-path fingerprint: if name+size+mtime
	// match the archive, the content is assumed unchanged and the sha256
	// is not recomputed.
	Size  int64 `json:"size"`
	Mtime int64 `json:"mtime"`
}
