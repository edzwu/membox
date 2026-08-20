package sqlite

import (
	"context"
	"fmt"

	"membox/internal/domain/catalog"
)

// LogicalID returns the visible short id for a physical document UUID.
// Physical ids in SQLite are never rewritten.
func (s *Store) LogicalID(ctx context.Context, physicalID string) (string, error) {
	if err := s.ensureLogicalIDs(ctx); err != nil {
		return "", err
	}
	s.logicalMu.RLock()
	defer s.logicalMu.RUnlock()
	return catalog.LookupLogical(physicalID, s.logicalByPhys), nil
}

// LogicalIDs returns a snapshot of physical → logical abbreviations.
func (s *Store) LogicalIDs(ctx context.Context) (map[string]string, error) {
	if err := s.ensureLogicalIDs(ctx); err != nil {
		return nil, err
	}
	s.logicalMu.RLock()
	defer s.logicalMu.RUnlock()
	out := make(map[string]string, len(s.logicalByPhys))
	for physical, logical := range s.logicalByPhys {
		out[physical] = logical
	}
	return out, nil
}

func (s *Store) invalidateLogicalIDs() {
	s.logicalMu.Lock()
	s.logicalDirty = true
	s.logicalMu.Unlock()
}

func (s *Store) ensureLogicalIDs(ctx context.Context) error {
	s.logicalMu.RLock()
	ready := s.logicalLoaded && !s.logicalDirty
	s.logicalMu.RUnlock()
	if ready {
		return nil
	}
	return s.refreshLogicalIDs(ctx)
}

func (s *Store) refreshLogicalIDs(ctx context.Context) error {
	ids, err := s.listAllDocumentIDs(ctx)
	if err != nil {
		return err
	}
	logical := catalog.LogicalIDs(ids)
	s.logicalMu.Lock()
	s.logicalByPhys = logical
	s.logicalLoaded = true
	s.logicalDirty = false
	s.logicalMu.Unlock()
	return nil
}

func (s *Store) listAllDocumentIDs(ctx context.Context) ([]string, error) {
	// Include trashed documents so restore-by-short-id and historical
	// references keep stable abbreviations across soft-delete.
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM documents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing document ids for logical abbreviations: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) logicalSnapshot(ctx context.Context) (map[string]string, error) {
	if err := s.ensureLogicalIDs(ctx); err != nil {
		return nil, err
	}
	s.logicalMu.RLock()
	defer s.logicalMu.RUnlock()
	// Callers must not mutate; return the live map under the assumption that
	// MatchLogicalSelector only reads. Copying 1k-10k entries per resolve is
	// unnecessary — ResolveDocument holds no write lock on the cache beyond
	// ensure. We copy to keep the API safe if the cache refreshes later.
	out := make(map[string]string, len(s.logicalByPhys))
	for k, v := range s.logicalByPhys {
		out[k] = v
	}
	return out, nil
}
