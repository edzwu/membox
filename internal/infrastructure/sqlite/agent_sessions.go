package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"membox/internal/agent"
)

// Ensure Store implements agent.SessionCatalog.
var _ agent.SessionCatalog = (*Store)(nil)

func (s *Store) ListSessions(ctx context.Context, includeArchived bool) ([]agent.SessionRecord, error) {
	query := `SELECT id, COALESCE(pi_session_id,''), session_path, title,
COALESCE(model_provider,''), COALESCE(model_id,''), COALESCE(thinking_level,''),
created_at, updated_at, last_used_at, archived
FROM agent_sessions`
	if !includeArchived {
		query += ` WHERE archived=0`
	}
	query += ` ORDER BY last_used_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing agent sessions: %w", err)
	}
	defer rows.Close()
	var out []agent.SessionRecord
	for rows.Next() {
		rec, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, id string) (agent.SessionRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(pi_session_id,''), session_path, title,
COALESCE(model_provider,''), COALESCE(model_id,''), COALESCE(thinking_level,''),
created_at, updated_at, last_used_at, archived
FROM agent_sessions WHERE id=?`, id)
	rec, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.SessionRecord{}, false, nil
	}
	if err != nil {
		return agent.SessionRecord{}, false, err
	}
	return rec, true, nil
}

func (s *Store) InsertSession(ctx context.Context, rec agent.SessionRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_sessions(
id, pi_session_id, session_path, title, model_provider, model_id, thinking_level,
created_at, updated_at, last_used_at, archived
) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, nullIfEmpty(rec.PiSessionID), rec.SessionPath, rec.Title,
		nullIfEmpty(rec.ModelProvider), nullIfEmpty(rec.ModelID), nullIfEmpty(rec.ThinkingLevel),
		rec.CreatedAt.Unix(), rec.UpdatedAt.Unix(), rec.LastUsedAt.Unix(), boolInt(rec.Archived),
	)
	if err != nil {
		return fmt.Errorf("inserting agent session: %w", err)
	}
	return nil
}

func (s *Store) UpdateSession(ctx context.Context, rec agent.SessionRecord) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET
pi_session_id=?, session_path=?, title=?, model_provider=?, model_id=?, thinking_level=?,
updated_at=?, last_used_at=?, archived=?
WHERE id=?`,
		nullIfEmpty(rec.PiSessionID), rec.SessionPath, rec.Title,
		nullIfEmpty(rec.ModelProvider), nullIfEmpty(rec.ModelID), nullIfEmpty(rec.ThinkingLevel),
		rec.UpdatedAt.Unix(), rec.LastUsedAt.Unix(), boolInt(rec.Archived), rec.ID,
	)
	if err != nil {
		return fmt.Errorf("updating agent session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent session %s not found", rec.ID)
	}
	return nil
}

func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET last_used_at=?, updated_at=? WHERE id=?`,
		at.Unix(), at.Unix(), id)
	if err != nil {
		return fmt.Errorf("touching agent session: %w", err)
	}
	return nil
}

func (s *Store) ArchiveSession(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET archived=1, updated_at=? WHERE id=?`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("archiving agent session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent session %s not found", id)
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSession(row scannable) (agent.SessionRecord, error) {
	var (
		rec                                        agent.SessionRecord
		created, updated, lastUsed                 int64
		archived                                   int
		piSID, modelProvider, modelID, thinking    string
	)
	err := row.Scan(
		&rec.ID, &piSID, &rec.SessionPath, &rec.Title,
		&modelProvider, &modelID, &thinking,
		&created, &updated, &lastUsed, &archived,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agent.SessionRecord{}, err
		}
		return agent.SessionRecord{}, fmt.Errorf("scanning agent session: %w", err)
	}
	rec.PiSessionID = piSID
	rec.ModelProvider = modelProvider
	rec.ModelID = modelID
	rec.ThinkingLevel = thinking
	rec.CreatedAt = time.Unix(created, 0).UTC()
	rec.UpdatedAt = time.Unix(updated, 0).UTC()
	rec.LastUsedAt = time.Unix(lastUsed, 0).UTC()
	rec.Archived = archived != 0
	return rec, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
