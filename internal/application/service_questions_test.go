package application_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"membox/internal/application"
	"membox/internal/application/port"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func newQuestionTestService(t *testing.T) *application.Service {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
}

func TestQuestionLifecycle(t *testing.T) {
	ctx := context.Background()
	service := newQuestionTestService(t)

	first, err := service.AddQuestion(ctx, application.AddQuestionCommand{Body: "为什么 FTS 是 AND？"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != port.QuestionOpen {
		t.Fatalf("status = %s", first.Status)
	}
	if _, err := service.AddQuestion(ctx, application.AddQuestionCommand{Body: "  "}); err == nil {
		t.Fatal("blank body must be rejected")
	}

	all, err := service.ListQuestions(ctx, "", 10)
	if err != nil || len(all) != 1 {
		t.Fatalf("list all = %v, %v", all, err)
	}

	answered, err := service.AnswerQuestion(ctx, application.AnswerQuestionCommand{Selector: first.ID, Answer: "FTS5 默认 AND"})
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != port.QuestionAnswered || answered.Answer != "FTS5 默认 AND" {
		t.Fatalf("answered = %+v", answered)
	}

	open, _ := service.ListQuestions(ctx, port.QuestionOpen, 10)
	if len(open) != 0 {
		t.Fatalf("open list should be empty, got %d", len(open))
	}

	// Short-suffix resolution.
	short := first.ID[len(first.ID)-6:]
	bySuffix, err := service.AnswerQuestion(ctx, application.AnswerQuestionCommand{Selector: short})
	if err != nil {
		t.Fatalf("suffix resolve: %v", err)
	}
	if bySuffix.ID != first.ID {
		t.Fatalf("suffix resolved wrong question: %s", bySuffix.ID)
	}

	// Archive + delete.
	archived, err := service.ArchiveQuestion(ctx, first.ID)
	if err != nil || archived.Status != port.QuestionArchived {
		t.Fatalf("archive = %+v, %v", archived, err)
	}
	if err := service.DeleteQuestion(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListQuestions(ctx, "", 10); err != nil {
		t.Fatal(err)
	}
}

func TestExtractQuestionsDeterministicMarkersAndSuffix(t *testing.T) {
	lines := []string{
		"- 需要关注, pi 的 cache 到底是怎么做的?",
		"  - Q: 为什么 FTS 默认 AND",
		"- opencli?",
		"- https://example.com/only",
		"- 问：向量库和 FTS 怎么配合",
		"* 需要关注, pi 的 cache 到底是怎么做的?", // same body, different bullet
		"- just a note without mark",
	}
	got := application.ExtractQuestions(lines)
	if len(got) != 3 {
		t.Fatalf("extracted = %#v (want 3)", got)
	}
	wantBodies := []string{
		"需要关注, pi 的 cache 到底是怎么做的?",
		"为什么 FTS 默认 AND",
		"向量库和 FTS 怎么配合",
	}
	for i, want := range wantBodies {
		if got[i].Body != want {
			t.Fatalf("body[%d] = %q, want %q", i, got[i].Body, want)
		}
	}
	// Canonical lowercases ASCII and collapses space.
	if application.CanonicalizeQuestionBody("  Hello   WORLD? ") != "hello world?" {
		t.Fatalf("canonical = %q", application.CanonicalizeQuestionBody("  Hello   WORLD? "))
	}
}

func TestQuestionIngestIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service := newQuestionTestService(t)
	lines := []string{
		"- online softmax 的对齐是怎么做的?",
		"- Q: 同一问题再次出现",
	}
	first, err := service.IngestQuestionLines(ctx, application.QuestionIngestOptions{
		Lines: lines, SourceFile: "/tmp/inbox.md", SourceCommit: "aaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Found != 2 || len(first.Inserted) != 2 || len(first.Existing) != 0 {
		t.Fatalf("first ingest = %+v inserted=%d existing=%d", first, len(first.Inserted), len(first.Existing))
	}
	second, err := service.IngestQuestionLines(ctx, application.QuestionIngestOptions{
		Lines: lines, SourceFile: "/tmp/inbox.md", SourceCommit: "bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Found != 2 || len(second.Inserted) != 0 || len(second.Existing) != 2 {
		t.Fatalf("second ingest = inserted=%d existing=%d", len(second.Inserted), len(second.Existing))
	}
	if second.Existing[0].SourceCommit != "bbb" && second.Existing[1].SourceCommit != "bbb" {
		t.Fatalf("expected provenance refresh, got %#v", second.Existing)
	}
	listed, err := service.ListQuestionsBySource(ctx, "/tmp/inbox.md", 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list by source = %v, %v", listed, err)
	}
}

func TestQuestionExportMarkdownGroupsByStatus(t *testing.T) {
	ctx := context.Background()
	service := newQuestionTestService(t)

	open, _ := service.AddQuestion(ctx, application.AddQuestionCommand{Body: "一个问题"})
	_, _ = service.AnswerQuestion(ctx, application.AnswerQuestionCommand{Selector: open.ID, Answer: "一个答案"})
	_, _ = service.AddQuestion(ctx, application.AddQuestionCommand{Body: "另一个问题"})

	md, err := service.ExportQuestionsMarkdown(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "## 未回答") || !strings.Contains(md, "## 已回答") {
		t.Fatalf("missing sections:\n%s", md)
	}
	if !strings.Contains(md, "一个答案") {
		t.Fatal("answer missing from export")
	}
	if !strings.Contains(md, "- [x]") {
		t.Fatalf("answered item should render [x]:\n%s", md)
	}
}
