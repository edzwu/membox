package remotetopics

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRepo implements Repository in memory.
type fakeRepo struct {
	topics  map[string]*Topic
	issues  map[string]*Issue
	members map[string]map[string]bool // topic → document set
	links   map[string]map[string]int  // topic → issue → position
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		topics:  map[string]*Topic{},
		issues:  map[string]*Issue{},
		members: map[string]map[string]bool{},
		links:   map[string]map[string]int{},
	}
}

func (f *fakeRepo) CreateTopic(_ context.Context, t Topic) error { f.topics[t.ID] = &t; return nil }

func (f *fakeRepo) ListTopics(_ context.Context, ownerID string) ([]Topic, error) {
	var out []Topic
	for _, t := range f.topics {
		if t.OwnerID == ownerID && !t.Deleted {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetTopic(_ context.Context, ownerID, id string) (Topic, error) {
	t, ok := f.topics[id]
	if !ok || t.OwnerID != ownerID || t.Deleted {
		return Topic{}, ErrNotFound
	}
	return *t, nil
}

func (f *fakeRepo) UpdateTopic(_ context.Context, ownerID, id, title, description string, now time.Time) error {
	t, err := f.GetTopic(context.Background(), ownerID, id)
	if err != nil {
		return err
	}
	t.Title, t.Description, t.UpdatedAt = title, description, now
	f.topics[id] = &t
	return nil
}

func (f *fakeRepo) DeleteTopic(_ context.Context, ownerID, id string, _ time.Time) error {
	t, err := f.GetTopic(context.Background(), ownerID, id)
	if err != nil {
		return err
	}
	t.Deleted = true
	f.topics[id] = &t
	delete(f.members, id)
	delete(f.links, id)
	return nil
}

func (f *fakeRepo) AddMember(_ context.Context, _, topicID, documentID string) error {
	if f.members[topicID] == nil {
		f.members[topicID] = map[string]bool{}
	}
	f.members[topicID][documentID] = true
	return nil
}

func (f *fakeRepo) RemoveMember(_ context.Context, _, topicID, documentID string) error {
	delete(f.members[topicID], documentID)
	return nil
}

func (f *fakeRepo) ListMembers(_ context.Context, _, topicID string) ([]string, error) {
	var ids []string
	for id := range f.members[topicID] {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *fakeRepo) CreateIssue(_ context.Context, i Issue) error { f.issues[i.ID] = &i; return nil }

func (f *fakeRepo) ListIssues(_ context.Context, ownerID, status string) ([]Issue, error) {
	var out []Issue
	for _, i := range f.issues {
		if i.OwnerID == ownerID && (status == "" || i.Status == status) {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetIssue(_ context.Context, ownerID, id string) (Issue, error) {
	i, ok := f.issues[id]
	if !ok || i.OwnerID != ownerID {
		return Issue{}, ErrNotFound
	}
	return *i, nil
}

func (f *fakeRepo) UpdateIssue(_ context.Context, ownerID, id, title, body, status string, now time.Time) error {
	i, err := f.GetIssue(context.Background(), ownerID, id)
	if err != nil {
		return err
	}
	i.Title, i.Body, i.Status, i.UpdatedAt = title, body, status, now
	if status == StatusAnswered && i.ResolvedAt == nil {
		i.ResolvedAt = &now
	}
	if status == StatusOpen {
		i.ResolvedAt = nil
	}
	f.issues[id] = &i
	return nil
}

func (f *fakeRepo) DeleteIssue(_ context.Context, ownerID, id string) error {
	if _, err := f.GetIssue(context.Background(), ownerID, id); err != nil {
		return err
	}
	delete(f.issues, id)
	for _, links := range f.links {
		delete(links, id)
	}
	return nil
}

func (f *fakeRepo) LinkIssue(_ context.Context, _, topicID, issueID string, position int) error {
	if f.links[topicID] == nil {
		f.links[topicID] = map[string]int{}
	}
	f.links[topicID][issueID] = position
	return nil
}

func (f *fakeRepo) UnlinkIssue(_ context.Context, _, topicID, issueID string) error {
	delete(f.links[topicID], issueID)
	return nil
}

func (f *fakeRepo) ListTopicIssues(_ context.Context, _, topicID string) ([]Issue, error) {
	var out []Issue
	for issueID := range f.links[topicID] {
		out = append(out, *f.issues[issueID])
	}
	return out, nil
}

type fakeDocs map[string]bool

func (d fakeDocs) Exists(_ context.Context, ownerID, id string) (bool, error) {
	return d[ownerID+"/"+id], nil
}

var seq = 0

func testIDs() (string, error) {
	seq++
	return time.Now().Format("150405.000000") + "-" + string(rune('a'+seq%26)), nil
}

func newTestService() (*Service, *fakeRepo, fakeDocs) {
	repo := newFakeRepo()
	docs := fakeDocs{"owner/doc-1": true}
	return NewService(repo, docs, testIDs), repo, docs
}

const owner = "owner"

func TestTopicLifecycle(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	topic, err := svc.CreateTopic(ctx, owner, "  tokenizer  ", "分词研究")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Title != "tokenizer" {
		t.Fatalf("title should be trimmed, got %q", topic.Title)
	}
	if _, err := svc.CreateTopic(ctx, owner, " ", ""); !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("empty title must fail, got %v", err)
	}
	if err := svc.DeleteTopic(ctx, owner, topic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetTopic(ctx, owner, topic.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted topic must be NotFound, got %v", err)
	}
}

func TestMemberRequiresOwnedDocument(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	topic, _ := svc.CreateTopic(ctx, owner, "t", "")
	if err := svc.AddMember(ctx, owner, topic.ID, "doc-1"); err != nil {
		t.Fatal(err)
	}
	// A document the owner does not have (or doesn't exist) is rejected.
	if err := svc.AddMember(ctx, owner, topic.ID, "doc-x"); !errors.Is(err, ErrForeignEntity) {
		t.Fatalf("want ErrForeignEntity, got %v", err)
	}
}

func TestIssueLifecycleAndStatus(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	issue, err := svc.CreateIssue(ctx, owner, "什么是 bpb?", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != StatusOpen {
		t.Fatalf("new issue should be open, got %q", issue.Status)
	}
	updated, err := svc.UpdateIssue(ctx, owner, issue.ID, "", "bits per byte", StatusAnswered)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusAnswered {
		t.Fatalf("got %q", updated.Status)
	}
	if _, err := svc.UpdateIssue(ctx, owner, issue.ID, "", "", "bogus"); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("bad status must fail, got %v", err)
	}
}

func TestIssueSpansTopicsAndSurvivesUnlink(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	a, _ := svc.CreateTopic(ctx, owner, "输入法", "")
	b, _ := svc.CreateTopic(ctx, owner, "LLM", "")
	issue, _ := svc.CreateIssue(ctx, owner, "中英日混合怎么做?", "", "", "")

	for _, topic := range []Topic{a, b} {
		if err := svc.LinkIssue(ctx, owner, topic.ID, issue.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, topic := range []Topic{a, b} {
		issues, _ := svc.ListTopicIssues(ctx, owner, topic.ID)
		if len(issues) != 1 {
			t.Fatalf("topic %s should list the issue", topic.Title)
		}
	}
	// Unlink from both topics: the issue entity itself survives (inbox semantics).
	_ = svc.UnlinkIssue(ctx, owner, a.ID, issue.ID)
	_ = svc.UnlinkIssue(ctx, owner, b.ID, issue.ID)
	all, _ := svc.ListIssues(ctx, owner, "")
	if len(all) != 1 {
		t.Fatalf("issue should survive unlinking from all topics")
	}
}

func TestDeleteTopicKeepsIssues(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	topic, _ := svc.CreateTopic(ctx, owner, "t", "")
	issue, _ := svc.CreateIssue(ctx, owner, "q", "", "", topic.ID)
	if err := svc.DeleteTopic(ctx, owner, topic.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.ListIssues(ctx, owner, "")
	if len(all) != 1 || all[0].ID != issue.ID {
		t.Fatal("deleting a topic must not delete its issues")
	}
}

func TestOwnerIsolation(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	topic, _ := svc.CreateTopic(ctx, owner, "t", "")
	if _, err := svc.GetTopic(ctx, "someone-else", topic.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign topic must be NotFound, got %v", err)
	}
}
