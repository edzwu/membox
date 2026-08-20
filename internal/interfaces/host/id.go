package host

import (
	"strings"
	"sync"

	"membox/internal/domain/catalog"
)

// logicalByPhysical is the process-wide display map for document abbreviations.
// The store owns physical UUIDs; Box/CLI refresh this snapshot so pure shortID()
// helpers stay corpus-aware without threading a Box through every call site.
var (
	logicalMu     sync.RWMutex
	logicalByPhys map[string]string
)

// SetLogicalIDs replaces the process-wide physical→logical abbreviation map.
// Pass nil to clear. Keys should be physical document UUIDs as stored in SQLite.
func SetLogicalIDs(physicalToLogical map[string]string) {
	logicalMu.Lock()
	defer logicalMu.Unlock()
	if physicalToLogical == nil {
		logicalByPhys = nil
		return
	}
	next := make(map[string]string, len(physicalToLogical))
	for physical, logical := range physicalToLogical {
		physical = strings.TrimSpace(physical)
		logical = strings.ToLower(strings.TrimSpace(logical))
		if physical == "" || logical == "" {
			continue
		}
		next[physical] = logical
		next[strings.ToLower(physical)] = logical
	}
	logicalByPhys = next
}

// ShortDocumentID displays the logical document id (unique compact suffix,
// usually 4–5 hex chars). Physical UUIDs remain the database identity; this
// is visibility only. When no corpus map is loaded, falls back to a fixed
// 4-char compact suffix.
func ShortDocumentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	logicalMu.RLock()
	logical := catalog.LookupLogical(id, logicalByPhys)
	logicalMu.RUnlock()
	return logical
}
