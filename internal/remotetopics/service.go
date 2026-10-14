// Package remotetopics implements topics (first-class collections) and
// issues (standalone questions). Entities attach to topics through relation
// tables — topic_members for documents, topic_issues for issues — so any
// entity can span topics and survives topic deletion. Status values mirror
// the local questions store (open/answered/archived) to keep future
// local↔remote sync lossless.
package remotetopics

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalidStatus = errors.New("status must be open, answered or archived")
	ErrTitleRequired = errors.New("title required")
	ErrForeignEntity = errors.New("referenced entity not found")
)

const (
	StatusOpen     = "open"
	StatusAnswered = "answered"
	StatusArchived = "archived"
)

type Topic struct {
	ID          string
	OwnerID     string
	Title       string
	Description string
	Deleted     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Issue struct {
	ID         string
	OwnerID    string
	DocumentID string // optional anchor to a document; empty = topic/global level
	Title      string
	Body       string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ResolvedAt *time.Time
}

// Repository is implemented by PostgreSQL. All reads/writes are owner-scoped;
// foreign entities read as ErrNotFound, never as permission errors.
type Repository interface {
	CreateTopic(ctx context.Context, t Topic) error
	ListTopics(ctx context.Context, ownerID string) ([]Topic, error)
	GetTopic(ctx context.Context, ownerID, id string) (Topic, error)
	UpdateTopic(ctx context.Context, ownerID, id, title, description string, now time.Time) error
	DeleteTopic(ctx context.Context, ownerID, id string, now time.Time) error // soft; cascades relations

	AddMember(ctx context.Context, ownerID, topicID, documentID string) error
	RemoveMember(ctx context.Context, ownerID, topicID, documentID string) error
	ListMembers(ctx context.Context, ownerID, topicID string) ([]string, error) // document IDs

	CreateIssue(ctx context.Context, i Issue) error
	ListIssues(ctx context.Context, ownerID, status string) ([]Issue, error)
	GetIssue(ctx context.Context, ownerID, id string) (Issue, error)
	UpdateIssue(ctx context.Context, ownerID, id, title, body, status string, now time.Time) error
	DeleteIssue(ctx context.Context, ownerID, id string) error // hard; cascades topic_issues

	LinkIssue(ctx context.Context, ownerID, topicID, issueID string, position int) error
	UnlinkIssue(ctx context.Context, ownerID, topicID, issueID string) error
	ListTopicIssues(ctx context.Context, ownerID, topicID string) ([]Issue, error)
}

// DocumentLookup verifies a document belongs to the owner before membership.
type DocumentLookup interface {
	Exists(ctx context.Context, ownerID, id string) (bool, error)
}

type Service struct {
	repo Repository
	docs DocumentLookup
	ids  func() (string, error)
}

func NewService(repo Repository, docs DocumentLookup, ids func() (string, error)) *Service {
	return &Service{repo: repo, docs: docs, ids: ids}
}

func validStatus(status string) bool {
	switch status {
	case StatusOpen, StatusAnswered, StatusArchived:
		return true
	}
	return false
}

// ---- topics ---------------------------------------------------------------

func (s *Service) CreateTopic(ctx context.Context, ownerID, title, description string) (Topic, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Topic{}, ErrTitleRequired
	}
	id, err := s.ids()
	if err != nil {
		return Topic{}, err
	}
	now := time.Now().UTC()
	topic := Topic{ID: id, OwnerID: ownerID, Title: title, Description: strings.TrimSpace(description), CreatedAt: now, UpdatedAt: now}
	return topic, s.repo.CreateTopic(ctx, topic)
}

func (s *Service) ListTopics(ctx context.Context, ownerID string) ([]Topic, error) {
	return s.repo.ListTopics(ctx, ownerID)
}

func (s *Service) GetTopic(ctx context.Context, ownerID, id string) (Topic, error) {
	return s.repo.GetTopic(ctx, ownerID, id)
}

func (s *Service) UpdateTopic(ctx context.Context, ownerID, id, title, description string) (Topic, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Topic{}, ErrTitleRequired
	}
	if err := s.repo.UpdateTopic(ctx, ownerID, id, title, strings.TrimSpace(description), time.Now().UTC()); err != nil {
		return Topic{}, err
	}
	return s.repo.GetTopic(ctx, ownerID, id)
}

func (s *Service) DeleteTopic(ctx context.Context, ownerID, id string) error {
	return s.repo.DeleteTopic(ctx, ownerID, id, time.Now().UTC())
}

// ---- membership -----------------------------------------------------------

func (s *Service) AddMember(ctx context.Context, ownerID, topicID, documentID string) error {
	if _, err := s.repo.GetTopic(ctx, ownerID, topicID); err != nil {
		return err
	}
	exists, err := s.docs.Exists(ctx, ownerID, documentID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrForeignEntity
	}
	return s.repo.AddMember(ctx, ownerID, topicID, documentID)
}

func (s *Service) RemoveMember(ctx context.Context, ownerID, topicID, documentID string) error {
	return s.repo.RemoveMember(ctx, ownerID, topicID, documentID)
}

func (s *Service) ListMembers(ctx context.Context, ownerID, topicID string) ([]string, error) {
	return s.repo.ListMembers(ctx, ownerID, topicID)
}

// ---- issues ---------------------------------------------------------------

func (s *Service) CreateIssue(ctx context.Context, ownerID, title, body, documentID, topicID string) (Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Issue{}, ErrTitleRequired
	}
	if documentID != "" {
		exists, err := s.docs.Exists(ctx, ownerID, documentID)
		if err != nil {
			return Issue{}, err
		}
		if !exists {
			return Issue{}, ErrForeignEntity
		}
	}
	if topicID != "" {
		if _, err := s.repo.GetTopic(ctx, ownerID, topicID); err != nil {
			return Issue{}, err
		}
	}
	id, err := s.ids()
	if err != nil {
		return Issue{}, err
	}
	now := time.Now().UTC()
	issue := Issue{
		ID: id, OwnerID: ownerID, DocumentID: documentID,
		Title: title, Body: body, Status: StatusOpen, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateIssue(ctx, issue); err != nil {
		return Issue{}, err
	}
	if topicID != "" {
		if err := s.repo.LinkIssue(ctx, ownerID, topicID, id, 0); err != nil {
			return Issue{}, err
		}
	}
	return issue, nil
}

func (s *Service) ListIssues(ctx context.Context, ownerID, status string) ([]Issue, error) {
	if status != "" && !validStatus(status) {
		return nil, ErrInvalidStatus
	}
	return s.repo.ListIssues(ctx, ownerID, status)
}

func (s *Service) UpdateIssue(ctx context.Context, ownerID, id, title, body, status string) (Issue, error) {
	current, err := s.repo.GetIssue(ctx, ownerID, id)
	if err != nil {
		return Issue{}, err
	}
	if title = strings.TrimSpace(title); title != "" {
		current.Title = title
	}
	current.Body = body
	if status != "" {
		if !validStatus(status) {
			return Issue{}, ErrInvalidStatus
		}
		current.Status = status
	}
	if err := s.repo.UpdateIssue(ctx, ownerID, id, current.Title, current.Body, current.Status, time.Now().UTC()); err != nil {
		return Issue{}, err
	}
	return s.repo.GetIssue(ctx, ownerID, id)
}

func (s *Service) DeleteIssue(ctx context.Context, ownerID, id string) error {
	return s.repo.DeleteIssue(ctx, ownerID, id)
}

func (s *Service) LinkIssue(ctx context.Context, ownerID, topicID, issueID string, position int) error {
	if _, err := s.repo.GetTopic(ctx, ownerID, topicID); err != nil {
		return err
	}
	if _, err := s.repo.GetIssue(ctx, ownerID, issueID); err != nil {
		return ErrForeignEntity
	}
	return s.repo.LinkIssue(ctx, ownerID, topicID, issueID, position)
}

func (s *Service) UnlinkIssue(ctx context.Context, ownerID, topicID, issueID string) error {
	return s.repo.UnlinkIssue(ctx, ownerID, topicID, issueID)
}

func (s *Service) ListTopicIssues(ctx context.Context, ownerID, topicID string) ([]Issue, error) {
	if _, err := s.repo.GetTopic(ctx, ownerID, topicID); err != nil {
		return nil, err
	}
	return s.repo.ListTopicIssues(ctx, ownerID, topicID)
}
