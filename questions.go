package membox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application"
	"membox/internal/application/port"
)

// QuestionView is the CLI/JSON-facing question shape.
type QuestionView struct {
	ID               string    `json:"id"`
	Body             string    `json:"body"`
	Status           string    `json:"status"`
	Answer           string    `json:"answer,omitempty"`
	SourceDocumentID string    `json:"source_document_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	AnsweredAt       time.Time `json:"answered_at,omitempty"`
}

func questionView(q port.Question) QuestionView {
	return QuestionView{
		ID: q.ID, Body: q.Body, Status: q.Status, Answer: q.Answer,
		SourceDocumentID: q.SourceDocumentID, CreatedAt: q.CreatedAt,
		UpdatedAt: q.UpdatedAt, AnsweredAt: q.AnsweredAt,
	}
}

type AddQuestionCommand struct {
	Body             string
	SourceDocumentID string
}

type ListQuestionsCommand struct {
	Status string
	Limit  int
}

type AnswerQuestionCommand struct {
	Selector string
	Answer   string
}

type ExportQuestionsCommand struct {
	Status string
	Out    string // "" = write questions-export.md in cwd
}

func (b *Box) AddQuestion(ctx context.Context, command AddQuestionCommand) (QuestionView, error) {
	question, err := b.service.AddQuestion(ctx, application.AddQuestionCommand{
		Body:             command.Body,
		SourceDocumentID: command.SourceDocumentID,
	})
	if err != nil {
		return QuestionView{}, err
	}
	return questionView(question), nil
}

func (b *Box) ListQuestions(ctx context.Context, command ListQuestionsCommand) ([]QuestionView, error) {
	questions, err := b.service.ListQuestions(ctx, command.Status, command.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]QuestionView, 0, len(questions))
	for _, q := range questions {
		out = append(out, questionView(q))
	}
	return out, nil
}

func (b *Box) AnswerQuestion(ctx context.Context, command AnswerQuestionCommand) (QuestionView, error) {
	question, err := b.service.AnswerQuestion(ctx, application.AnswerQuestionCommand{
		Selector: command.Selector,
		Answer:   command.Answer,
	})
	if err != nil {
		return QuestionView{}, err
	}
	return questionView(question), nil
}

func (b *Box) ArchiveQuestion(ctx context.Context, selector string) (QuestionView, error) {
	question, err := b.service.ArchiveQuestion(ctx, selector)
	if err != nil {
		return QuestionView{}, err
	}
	return questionView(question), nil
}

func (b *Box) DeleteQuestion(ctx context.Context, selector string) error {
	return b.service.DeleteQuestion(ctx, selector)
}

func (b *Box) UpdateQuestionBody(ctx context.Context, selector, body string) (QuestionView, error) {
	question, err := b.service.UpdateQuestionBody(ctx, application.UpdateQuestionCommand{Selector: selector, Body: body})
	if err != nil {
		return QuestionView{}, err
	}
	return questionView(question), nil
}

// ExportQuestions writes the Markdown export and returns its path.
func (b *Box) ExportQuestions(ctx context.Context, command ExportQuestionsCommand) (string, error) {
	markdown, err := b.service.ExportQuestionsMarkdown(ctx, command.Status)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(command.Out)
	if out == "" {
		out = "questions-export.md"
	}
	absolute, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(absolute, []byte(markdown), 0o644); err != nil {
		return "", fmt.Errorf("writing question export: %w", err)
	}
	return absolute, nil
}
