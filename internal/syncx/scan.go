package syncx

import (
	"path"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

// EntryFromRecord maps one catalog record to its sync Entry. Pure function.
//
// Rules:
//   - Exists: only active documents outside the per-root trash directory.
//     Missing, untracked and trashed documents are all tombstones — deleted
//     content must propagate, not silently resurrect on the other side.
//   - Name: basename of the relative path. Location.RelativePath is
//     slash-normalized by catalog.NewLocation, so path.Base is correct on
//     every OS (never filepath.Base here).
//   - Fingerprint fields are copied verbatim from the index state.
//
// A nil record yields the zero Entry; ScanCatalog skips such records.
func EntryFromRecord(r port.DocumentRecord) Entry {
	d := r.Document
	if d == nil {
		return Entry{}
	}
	return Entry{
		Exists: d.Status == catalog.DocumentActive &&
			!catalog.IsTrashedPath(d.Location.RelativePath),
		Name:          path.Base(d.Location.RelativePath),
		ContentSha256: d.Index.SHA256,
		Size:          d.Index.Size,
		Mtime:         d.Index.MTime,
	}
}

// ScanCatalog builds the sync snapshot: physical UUID → Entry. Records with
// a nil document are skipped. Callers feed the records (from
// Service.ListDocuments and, for tombstones, trashed-document listings);
// this function performs no IO.
func ScanCatalog(docs []port.DocumentRecord) map[string]Entry {
	entries := make(map[string]Entry, len(docs))
	for _, r := range docs {
		if r.Document == nil {
			continue
		}
		entries[string(r.Document.ID)] = EntryFromRecord(r)
	}
	return entries
}
