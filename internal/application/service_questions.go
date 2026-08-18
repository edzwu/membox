package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"membox/internal/application/port"
)

// Question service: personal question accumulation with optional answers and
// a link back to the document the question arose from.

type AddQuestionCommand struct {
	Body             string
	SourceDocumentID string
}

type AnswerQuestionCommand struct {
	Selector string
	Answer   string
	Status   string // "" keeps current; port.QuestionAnswered or archived to move
}

type UpdateQuestionCommand struct {
	Selector string
	Body     string
}

type QuestionView struct {
	port.Question
}

func (s *Service) questions() (port.QuestionStore, error) {
	store := s.store.Questions()
	if store == nil {
		return nil, errors.New("question store is unavailable")
	}
	return store, nil
}

// AddQuestion records a new open question. SourceDocumentID is optional.
func (s *Service) AddQuestion(ctx context.Context, cmd AddQuestionCommand) (port.Question, error) {
	body := strings.TrimSpace(cmd.Body)
	if body == "" {
		return port.Question{}, errors.New("question body is required")
	}
	if len([]rune(body)) > 2000 {
		return port.Question{}, errors.New("question body too long (max 2000 characters)")
	}
	store, err := s.questions()
	if err != nil {
		return port.Question{}, err
	}
	id, err := s.ids.NewDocumentID()
	if err != nil {
		return port.Question{}, fmt.Errorf("generating question id: %w", err)
	}
	sourceID := strings.TrimSpace(cmd.SourceDocumentID)
	if sourceID != "" {
		if _, _, err := s.ResolveDocument(ctx, sourceID); err != nil {
			return port.Question{}, fmt.Errorf("source document: %w", err)
		}
	}
	return store.Add(ctx, port.Question{
		ID:               string(id),
		Body:             body,
		CanonicalBody:    CanonicalizeQuestionBody(body),
		Status:           port.QuestionOpen,
		SourceDocumentID: sourceID,
	})
}

// ListQuestions returns questions, open first then newest.
func (s *Service) ListQuestions(ctx context.Context, status string, limit int) ([]port.Question, error) {
	if status == "" {
		status = ""
	}
	if status != "" && status != port.QuestionOpen && status != port.QuestionAnswered && status != port.QuestionArchived {
		return nil, fmt.Errorf("invalid status %q (want open|answered|archived)", status)
	}
	store, err := s.questions()
	if err != nil {
		return nil, err
	}
	return store.List(ctx, port.QuestionListQuery{Status: status, Limit: limit})
}

// AnswerQuestion resolves a question with an answer (or archives without one).
func (s *Service) AnswerQuestion(ctx context.Context, cmd AnswerQuestionCommand) (port.Question, error) {
	store, err := s.questions()
	if err != nil {
		return port.Question{}, err
	}
	current, err := store.Resolve(ctx, cmd.Selector)
	if err != nil {
		return port.Question{}, err
	}
	if strings.TrimSpace(cmd.Answer) != "" {
		current.Answer = strings.TrimSpace(cmd.Answer)
		current.Status = port.QuestionAnswered
		if current.AnsweredAt.IsZero() {
			current.AnsweredAt = time.Now()
		}
	} else if cmd.Status == port.QuestionArchived {
		current.Status = port.QuestionArchived
	} else if cmd.Status == port.QuestionAnswered {
		current.Status = port.QuestionAnswered
	}
	return store.Update(ctx, current)
}

// UpdateQuestionBody edits a question's text.
func (s *Service) UpdateQuestionBody(ctx context.Context, cmd UpdateQuestionCommand) (port.Question, error) {
	store, err := s.questions()
	if err != nil {
		return port.Question{}, err
	}
	current, err := store.Resolve(ctx, cmd.Selector)
	if err != nil {
		return port.Question{}, err
	}
	body := strings.TrimSpace(cmd.Body)
	if body == "" {
		return port.Question{}, errors.New("question body is required")
	}
	current.Body = body
	current.CanonicalBody = CanonicalizeQuestionBody(body)
	return store.Update(ctx, current)
}

// ArchiveQuestion marks a question archived without an answer.
func (s *Service) ArchiveQuestion(ctx context.Context, selector string) (port.Question, error) {
	return s.AnswerQuestion(ctx, AnswerQuestionCommand{Selector: selector, Status: port.QuestionArchived})
}

// DeleteQuestion removes a question entirely.
func (s *Service) DeleteQuestion(ctx context.Context, selector string) error {
	store, err := s.questions()
	if err != nil {
		return err
	}
	question, err := store.Resolve(ctx, selector)
	if err != nil {
		return err
	}
	return store.Delete(ctx, question.ID)
}

// ExportQuestionsMarkdown renders all questions (or one status) as a readable
// Markdown document grouped by status, newest first.
func (s *Service) ExportQuestionsMarkdown(ctx context.Context, status string) (string, error) {
	store, err := s.questions()
	if err != nil {
		return "", err
	}
	limit := 500
	var all []port.Question
	if status == "" || status == port.QuestionOpen {
		all, err = store.List(ctx, port.QuestionListQuery{Status: status, Limit: limit})
	} else {
		all, err = store.List(ctx, port.QuestionListQuery{Status: status, Limit: limit})
	}
	if err != nil {
		return "", err
	}
	// Deduplicate when status empty already returns all; group by status.
	grouped := map[string][]port.Question{}
	for _, q := range all {
		grouped[q.Status] = append(grouped[q.Status], q)
	}
	var b strings.Builder
	b.WriteString("# 问题清单\n\n")
	b.WriteString("> 由 membox 导出 · ")
	b.WriteString(time.Now().Format("2006-01-02 15:04"))
	b.WriteString("\n\n")

	section := func(title, key string) {
		items := grouped[key]
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s（%d）\n\n", title, len(items))
		for _, q := range items {
			mark := "[ ]"
			if q.Status == port.QuestionAnswered {
				mark = "[x]"
			} else if q.Status == port.QuestionArchived {
				mark = "[~]"
			}
			fmt.Fprintf(&b, "- %s **%s** ", mark, q.CreatedAt.Format("2006-01-02"))
			b.WriteString(strings.ReplaceAll(q.Body, "\n", " "))
			if q.SourceDocumentID != "" {
				fmt.Fprintf(&b, " — [来源](?id=%s)", q.SourceDocumentID)
			}
			b.WriteString("\n")
			if strings.TrimSpace(q.Answer) != "" {
				fmt.Fprintf(&b, "  - 答：%s\n", strings.ReplaceAll(strings.TrimSpace(q.Answer), "\n", " "))
			}
		}
		b.WriteString("\n")
	}
	section("未回答", port.QuestionOpen)
	section("已回答", port.QuestionAnswered)
	section("已归档", port.QuestionArchived)
	return b.String(), nil
}
