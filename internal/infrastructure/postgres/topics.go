package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"membox/internal/remotetopics"
)

// TopicStore implements remotetopics.Repository plus DocumentLookup.
type TopicStore struct {
	pool *pgxpool.Pool
}

func NewTopicStore(pool *pgxpool.Pool) *TopicStore { return &TopicStore{pool: pool} }

// Exists implements remotetopics.DocumentLookup against the documents table.
func (s *TopicStore) Exists(ctx context.Context, ownerID, id string) (bool, error) {
	var one int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM documents WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL`,
		ownerID, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ---- topics ---------------------------------------------------------------

func (s *TopicStore) CreateTopic(ctx context.Context, t remotetopics.Topic) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO topics(id, owner_id, title, description, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.OwnerID, t.Title, t.Description, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *TopicStore) ListTopics(ctx context.Context, ownerID string) ([]remotetopics.Topic, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, description, created_at, updated_at
		FROM topics
		WHERE owner_id = $1 AND deleted_at IS NULL
		ORDER BY updated_at DESC, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var topics []remotetopics.Topic
	for rows.Next() {
		var t remotetopics.Topic
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.OwnerID = ownerID
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

func (s *TopicStore) GetTopic(ctx context.Context, ownerID, id string) (remotetopics.Topic, error) {
	var t remotetopics.Topic
	err := s.pool.QueryRow(ctx, `
		SELECT id, title, description, created_at, updated_at
		FROM topics
		WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL`, ownerID, id,
	).Scan(&t.ID, &t.Title, &t.Description, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return remotetopics.Topic{}, remotetopics.ErrNotFound
	}
	t.OwnerID = ownerID
	return t, err
}

func (s *TopicStore) UpdateTopic(ctx context.Context, ownerID, id, title, description string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE topics SET title = $3, description = $4, updated_at = $5
		WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL`,
		ownerID, id, title, description, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return remotetopics.ErrNotFound
	}
	return nil
}

// DeleteTopic soft-deletes the topic and removes its relation rows; member
// documents and linked issues themselves survive (entities outlive topics).
func (s *TopicStore) DeleteTopic(ctx context.Context, ownerID, id string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE topics SET deleted_at = $3, updated_at = $3
		WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL`, ownerID, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return remotetopics.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM topic_members WHERE topic_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM topic_issues WHERE topic_id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- membership -----------------------------------------------------------

func (s *TopicStore) AddMember(ctx context.Context, ownerID, topicID, documentID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO topic_members(topic_id, document_id)
		SELECT $1, $2 WHERE EXISTS (
			SELECT 1 FROM topics WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL)
		ON CONFLICT (topic_id, document_id) DO NOTHING`,
		topicID, documentID, ownerID)
	return err
}

func (s *TopicStore) RemoveMember(ctx context.Context, ownerID, topicID, documentID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM topic_members
		WHERE topic_id = $1 AND document_id = $2
		  AND EXISTS (SELECT 1 FROM topics WHERE id = $1 AND owner_id = $3)`,
		topicID, documentID, ownerID)
	return err
}

func (s *TopicStore) ListMembers(ctx context.Context, ownerID, topicID string) ([]string, error) {
	if _, err := s.GetTopic(ctx, ownerID, topicID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT document_id FROM topic_members
		WHERE topic_id = $1 ORDER BY added_at, document_id`, topicID)
	if err != nil {
		return nil, err
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

// ---- issues ---------------------------------------------------------------

func (s *TopicStore) CreateIssue(ctx context.Context, i remotetopics.Issue) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO issues(id, owner_id, document_id, title, body, status, created_at, updated_at)
		VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8)`,
		i.ID, i.OwnerID, i.DocumentID, i.Title, i.Body, i.Status, i.CreatedAt, i.UpdatedAt)
	return err
}

const issueSelect = `
	SELECT id, COALESCE(document_id::text,''), title, body, status,
	       created_at, updated_at, resolved_at
	FROM issues`

func scanIssue(row interface{ Scan(...any) error }, i *remotetopics.Issue, ownerID string) error {
	err := row.Scan(&i.ID, &i.DocumentID, &i.Title, &i.Body, &i.Status,
		&i.CreatedAt, &i.UpdatedAt, &i.ResolvedAt)
	i.OwnerID = ownerID
	return err
}

func (s *TopicStore) ListIssues(ctx context.Context, ownerID, status string) ([]remotetopics.Issue, error) {
	query := issueSelect + ` WHERE owner_id = $1`
	args := []any{ownerID}
	if status != "" {
		args = append(args, status)
		query += ` AND status = $2`
	}
	query += ` ORDER BY updated_at DESC, id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []remotetopics.Issue
	for rows.Next() {
		var i remotetopics.Issue
		if err := scanIssue(rows, &i, ownerID); err != nil {
			return nil, err
		}
		issues = append(issues, i)
	}
	return issues, rows.Err()
}

func (s *TopicStore) GetIssue(ctx context.Context, ownerID, id string) (remotetopics.Issue, error) {
	var i remotetopics.Issue
	err := scanIssue(s.pool.QueryRow(ctx, issueSelect+` WHERE owner_id = $1 AND id = $2`, ownerID, id), &i, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return remotetopics.Issue{}, remotetopics.ErrNotFound
	}
	return i, err
}

// UpdateIssue stamps resolved_at on the open→answered transition and clears
// it on reopen; archiving keeps the resolved timestamp as history.
func (s *TopicStore) UpdateIssue(ctx context.Context, ownerID, id, title, body, status string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE issues SET title = $3, body = $4, status = $5, updated_at = $6,
		    resolved_at = CASE
		        WHEN $5 = 'answered' AND resolved_at IS NULL THEN $6
		        WHEN $5 = 'open' THEN NULL
		        ELSE resolved_at END
		WHERE owner_id = $1 AND id = $2`,
		ownerID, id, title, body, status, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return remotetopics.ErrNotFound
	}
	return nil
}

func (s *TopicStore) DeleteIssue(ctx context.Context, ownerID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM topic_issues WHERE issue_id = $1`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM issues WHERE owner_id = $1 AND id = $2`, ownerID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return remotetopics.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *TopicStore) LinkIssue(ctx context.Context, ownerID, topicID, issueID string, position int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO topic_issues(topic_id, issue_id, position)
		SELECT $1, $2, $4
		WHERE EXISTS (SELECT 1 FROM topics WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL)
		  AND EXISTS (SELECT 1 FROM issues WHERE id = $2 AND owner_id = $3)
		ON CONFLICT (topic_id, issue_id) DO UPDATE SET position = $4`,
		topicID, issueID, ownerID, position)
	return err
}

func (s *TopicStore) UnlinkIssue(ctx context.Context, ownerID, topicID, issueID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM topic_issues
		WHERE topic_id = $1 AND issue_id = $2
		  AND EXISTS (SELECT 1 FROM topics WHERE id = $1 AND owner_id = $3)`,
		topicID, issueID, ownerID)
	return err
}

func (s *TopicStore) ListTopicIssues(ctx context.Context, ownerID, topicID string) ([]remotetopics.Issue, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, COALESCE(i.document_id::text,''), i.title, i.body, i.status,
		       i.created_at, i.updated_at, i.resolved_at
		FROM topic_issues ti JOIN issues i ON i.id = ti.issue_id
		WHERE ti.topic_id = $1 AND i.owner_id = $2
		ORDER BY ti.position, ti.added_at, i.id`, topicID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []remotetopics.Issue
	for rows.Next() {
		var i remotetopics.Issue
		if err := scanIssue(rows, &i, ownerID); err != nil {
			return nil, err
		}
		issues = append(issues, i)
	}
	return issues, rows.Err()
}
