package sqlite

import (
	"context"
	"fmt"
	"time"

	"membox/internal/application/port"
)

// ListReviewCards returns every active note card (annotations not in trash),
// newest first, with its schedule state. The frontend mixes new/due/aging.
func (s *Store) ListReviewCards(ctx context.Context) ([]port.ReviewCard, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.note_document_id, a.target_document_id, COALESCE(a.kind,''),
       a.highlight, a.underline, a.strikethrough, a.created_at, a.updated_at,
       COALESCE(c.due_at,0), COALESCE(c.interval_days,0), COALESCE(c.ease,2.5),
       COALESCE(c.reps,0), COALESCE(c.lapses,0), COALESCE(c.last_reviewed_at,0)
FROM annotation_notes a
JOIN document_locations l ON l.document_id = a.note_document_id
LEFT JOIN card_schedule c ON c.note_document_id = a.note_document_id
WHERE l.status = 'active'
  AND a.note_document_id NOT IN (SELECT document_id FROM document_trash)
ORDER BY a.created_at DESC, a.note_document_id`)
	if err != nil {
		return nil, fmt.Errorf("listing review cards: %w", err)
	}
	defer rows.Close()
	var cards []port.ReviewCard
	for rows.Next() {
		var card port.ReviewCard
		var hl, ul, sl int
		var createdAt, updatedAt int64
		var dueAt, interval, reps, lapses, lastReviewed int64
		var ease float64
		if err := rows.Scan(&card.NoteDocumentID, &card.TargetDocumentID, &card.Kind,
			&hl, &ul, &sl, &createdAt, &updatedAt,
			&dueAt, &interval, &ease, &reps, &lapses, &lastReviewed); err != nil {
			return nil, fmt.Errorf("scanning review card: %w", err)
		}
		card.Highlight = hl != 0
		card.Underline = ul != 0
		card.Strikethrough = sl != 0
		card.CreatedAt = time.UnixMilli(createdAt).UTC()
		card.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		card.Schedule = port.CardSchedule{
			NoteDocumentID: card.NoteDocumentID,
			DueAt:          dueAt,
			IntervalDays:   interval,
			Ease:           ease,
			Reps:           reps,
			Lapses:         lapses,
			LastReviewedAt: lastReviewed,
		}
		cards = append(cards, card)
	}
	return cards, rows.Err()
}

// SaveCardSchedule upserts one card's spaced-review state.
func (s *Store) SaveCardSchedule(ctx context.Context, schedule port.CardSchedule) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO card_schedule(note_document_id, due_at, interval_days, ease, reps, lapses, last_reviewed_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(note_document_id) DO UPDATE SET
  due_at=excluded.due_at,
  interval_days=excluded.interval_days,
  ease=excluded.ease,
  reps=excluded.reps,
  lapses=excluded.lapses,
  last_reviewed_at=excluded.last_reviewed_at`,
		schedule.NoteDocumentID, schedule.DueAt, schedule.IntervalDays, schedule.Ease,
		schedule.Reps, schedule.Lapses, schedule.LastReviewedAt)
	if err != nil {
		return fmt.Errorf("saving card schedule %s: %w", schedule.NoteDocumentID, err)
	}
	return nil
}

// ListScheduledCardIDs returns note document IDs that have a schedule row
// (used to distinguish new vs. seen cards in the queue mix).
func (s *Store) ListScheduledCardIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT note_document_id FROM card_schedule`)
	if err != nil {
		return nil, fmt.Errorf("listing scheduled cards: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	return seen, rows.Err()
}
