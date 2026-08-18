package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"membox/internal/application/port"
)

// QuestionStore implements the question backstop (port.QuestionStore) over
// SQLite. Questions are lightweight: a body, optional answer/status, optional
// document link, plus inbox-scan provenance and a canonical_body dedupe key.
type QuestionStore struct {
	db *sql.DB
}

func (s *Store) Questions() port.QuestionStore {
	return QuestionStore{db: s.db}
}

const questionColumns = `id, body, COALESCE(canonical_body,''), status, answer,
COALESCE(source_document_id,''), COALESCE(source_file,''), COALESCE(source_line,''),
COALESCE(source_commit,''), created_at, updated_at, COALESCE(answered_at,0)`

func (s QuestionStore) scanQuestion(row interface{ Scan(...any) error }) (port.Question, error) {
	var q port.Question
	var sourceID sql.NullString
	var createdAt, updatedAt, answeredAt sql.NullInt64
	if err := row.Scan(
		&q.ID, &q.Body, &q.CanonicalBody, &q.Status, &q.Answer, &sourceID,
		&q.SourceFile, &q.SourceLine, &q.SourceCommit,
		&createdAt, &updatedAt, &answeredAt,
	); err != nil {
		return port.Question{}, err
	}
	q.SourceDocumentID = sourceID.String
	q.CreatedAt = time.UnixMilli(createdAt.Int64)
	q.UpdatedAt = time.UnixMilli(updatedAt.Int64)
	if answeredAt.Valid && answeredAt.Int64 > 0 {
		q.AnsweredAt = time.UnixMilli(answeredAt.Int64)
	}
	return q, nil
}

func (s QuestionStore) Add(ctx context.Context, q port.Question) (port.Question, error) {
	now := time.Now()
	q.ID = strings.TrimSpace(q.ID)
	q.Body = strings.TrimSpace(q.Body)
	q.CanonicalBody = strings.TrimSpace(q.CanonicalBody)
	if q.ID == "" {
		return port.Question{}, errors.New("question id is required")
	}
	if q.Body == "" {
		return port.Question{}, errors.New("question body is required")
	}
	if q.Status == "" {
		q.Status = port.QuestionOpen
	}
	if !validQuestionStatus(q.Status) {
		return port.Question{}, fmt.Errorf("invalid question status %q", q.Status)
	}
	q.CreatedAt = now
	q.UpdatedAt = now
	if q.SourceDocumentID != "" && !questionUUID(q.SourceDocumentID) {
		return port.Question{}, errors.New("source_document_id must be a UUID")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO questions
(id, body, canonical_body, status, answer, source_document_id, source_file, source_line, source_commit, created_at, updated_at, answered_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		q.ID, q.Body, q.CanonicalBody, q.Status, strings.TrimSpace(q.Answer), nullString(q.SourceDocumentID),
		strings.TrimSpace(q.SourceFile), strings.TrimSpace(q.SourceLine), strings.TrimSpace(q.SourceCommit),
		q.CreatedAt.UnixMilli(), q.UpdatedAt.UnixMilli(), nullInt64Millis(q.AnsweredAt))
	if err != nil {
		return port.Question{}, fmt.Errorf("adding question: %w", err)
	}
	return q, nil
}

// Ingest inserts questions by canonical_body (INSERT OR IGNORE). Existing rows
// keep their status/answer; provenance is refreshed when the row already exists
// so the latest inbox line remains discoverable.
func (s QuestionStore) Ingest(ctx context.Context, questions []port.QuestionInsert) ([]port.QuestionIngestRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	out := make([]port.QuestionIngestRecord, 0, len(questions))
	for _, input := range questions {
		body := strings.TrimSpace(input.Body)
		canonical := strings.TrimSpace(input.CanonicalBody)
		if body == "" || canonical == "" {
			continue
		}
		createdAt := input.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		ms := createdAt.UnixMilli()
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO questions
(id, body, canonical_body, status, answer, source_document_id, source_file, source_line, source_commit, created_at, updated_at, answered_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,NULL)`,
			strings.TrimSpace(input.ID), body, canonical, port.QuestionOpen, "",
			nullString(input.SourceDocumentID),
			strings.TrimSpace(input.SourceFile), strings.TrimSpace(input.SourceLine), strings.TrimSpace(input.SourceCommit),
			ms, ms)
		if err != nil {
			return nil, fmt.Errorf("ingesting question %q: %w", canonical, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		inserted := rows > 0
		if !inserted {
			// Refresh latest inbox provenance without clobbering answers/status.
			if _, err := tx.ExecContext(ctx, `UPDATE questions SET
source_document_id = COALESCE(NULLIF(?, ''), source_document_id),
source_file = CASE WHEN ? != '' THEN ? ELSE source_file END,
source_line = CASE WHEN ? != '' THEN ? ELSE source_line END,
source_commit = CASE WHEN ? != '' THEN ? ELSE source_commit END,
updated_at = ?
WHERE canonical_body = ?`,
				strings.TrimSpace(input.SourceDocumentID),
				strings.TrimSpace(input.SourceFile), strings.TrimSpace(input.SourceFile),
				strings.TrimSpace(input.SourceLine), strings.TrimSpace(input.SourceLine),
				strings.TrimSpace(input.SourceCommit), strings.TrimSpace(input.SourceCommit),
				ms, canonical); err != nil {
				return nil, fmt.Errorf("updating question provenance %q: %w", canonical, err)
			}
		}
		record, err := s.scanQuestion(tx.QueryRowContext(ctx,
			`SELECT `+questionColumns+` FROM questions WHERE canonical_body = ?`, canonical))
		if err != nil {
			return nil, err
		}
		out = append(out, port.QuestionIngestRecord{Question: record, Inserted: inserted})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s QuestionStore) List(ctx context.Context, query port.QuestionListQuery) ([]port.Question, error) {
	where := []string{}
	args := []any{}
	if query.Status != "" {
		if !validQuestionStatus(query.Status) {
			return nil, fmt.Errorf("invalid question status %q", query.Status)
		}
		where = append(where, `status = ?`)
		args = append(args, query.Status)
	}
	if strings.TrimSpace(query.SourceDocumentID) != "" {
		where = append(where, `source_document_id = ?`)
		args = append(args, query.SourceDocumentID)
	}
	if strings.TrimSpace(query.SourceFile) != "" {
		where = append(where, `source_file = ?`)
		args = append(args, query.SourceFile)
	}
	sqlWhere := ""
	if len(where) > 0 {
		sqlWhere = "WHERE " + strings.Join(where, " AND ")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+questionColumns+` FROM questions `+sqlWhere+`
ORDER BY status = 'open' DESC, created_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("listing questions: %w", err)
	}
	defer rows.Close()
	var out []port.Question
	for rows.Next() {
		q, err := s.scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s QuestionStore) Resolve(ctx context.Context, selector string) (port.Question, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return port.Question{}, errors.New("question selector is required")
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id = ?`, selector)
	q, err := s.scanQuestion(row)
	if err == nil {
		return q, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return port.Question{}, err
	}
	// Unique short-suffix fallback (CLI/agent ergonomics).
	row = s.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id LIKE ? ORDER BY created_at DESC LIMIT 1`, "%"+selector)
	q, err = s.scanQuestion(row)
	if err != nil {
		return port.Question{}, fmt.Errorf("question %q not found", selector)
	}
	return q, nil
}

func (s QuestionStore) Update(ctx context.Context, q port.Question) (port.Question, error) {
	q.ID = strings.TrimSpace(q.ID)
	if q.ID == "" {
		return port.Question{}, errors.New("question id is required")
	}
	if !validQuestionStatus(q.Status) {
		return port.Question{}, fmt.Errorf("invalid question status %q", q.Status)
	}
	q.UpdatedAt = time.Now()
	var answeredAt any
	if q.Status == port.QuestionAnswered && q.AnsweredAt.IsZero() {
		q.AnsweredAt = q.UpdatedAt
	}
	answeredAt = nullInt64Millis(q.AnsweredAt)
	_, err := s.db.ExecContext(ctx, `UPDATE questions SET body=?, canonical_body=COALESCE(NULLIF(?, ''), canonical_body),
status=?, answer=?, source_document_id=?, source_file=?, source_line=?, source_commit=?, updated_at=?, answered_at=?
WHERE id=?`,
		strings.TrimSpace(q.Body), strings.TrimSpace(q.CanonicalBody),
		q.Status, strings.TrimSpace(q.Answer), nullString(q.SourceDocumentID),
		strings.TrimSpace(q.SourceFile), strings.TrimSpace(q.SourceLine), strings.TrimSpace(q.SourceCommit),
		q.UpdatedAt.UnixMilli(), answeredAt, q.ID)
	if err != nil {
		return port.Question{}, fmt.Errorf("updating question: %w", err)
	}
	return q, nil
}

func (s QuestionStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM questions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting question: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("question %q not found", id)
	}
	return nil
}

func (s QuestionStore) Count(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM questions GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("counting questions: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}

func validQuestionStatus(status string) bool {
	return status == port.QuestionOpen || status == port.QuestionAnswered || status == port.QuestionArchived
}

func questionUUID(value string) bool {
	for _, r := range strings.TrimSpace(value) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-' {
			continue
		}
		return false
	}
	return len(strings.TrimSpace(value)) >= 8
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullInt64Millis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
