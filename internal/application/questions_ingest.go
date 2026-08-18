package application

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"membox/internal/application/port"
)

// Deterministic inbox → question extraction (mirrors ExtractResourceURLs).
//
// A line becomes a question when, after stripping list/checkbox decorations:
//  1. it has an explicit marker: Q: / q: / question: / 问: / 问：
//  2. OR it ends with ? / ？ and looks like a real question (not a one-token tag)
//
// Pure URL lines and empty bodies are skipped. Identity is CanonicalizeQuestionBody.

var (
	questionListMarker = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	questionCheckbox   = regexp.MustCompile(`(?i)^\s*\[(?: |x)\]\s*`)
	questionPrefix     = regexp.MustCompile(`(?i)^\s*(?:Q|question|问)\s*[:：]\s*(.+)$`)
)

// ExtractedQuestion is one question body plus the inbox line that supplied it.
type ExtractedQuestion struct {
	Body       string
	Canonical  string
	SourceLine string
}

// CanonicalizeQuestionBody builds the stable dedupe key owned by membox.
func CanonicalizeQuestionBody(body string) string {
	fields := strings.Fields(strings.TrimSpace(body))
	if len(fields) == 0 {
		return ""
	}
	joined := strings.Join(fields, " ")
	var b strings.Builder
	b.Grow(len(joined))
	for _, r := range joined {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripQuestionDecorations(line string) string {
	s := strings.TrimSpace(line)
	for {
		next := questionListMarker.ReplaceAllString(s, "")
		next = questionCheckbox.ReplaceAllString(next, "")
		next = strings.TrimSpace(next)
		if next == s {
			return s
		}
		s = next
	}
}

func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}

func isURLOnlyLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	// Markdown link or bare URL covering essentially the whole line.
	if markdownURLPattern.MatchString(trimmed) {
		without := markdownURLPattern.ReplaceAllString(trimmed, "")
		without = strings.TrimSpace(without)
		return without == "" || without == "-" || without == "*"
	}
	if webURLPattern.MatchString(trimmed) {
		without := webURLPattern.ReplaceAllString(trimmed, "")
		without = strings.TrimSpace(without)
		return without == "" || without == "-" || without == "*"
	}
	return false
}

func looksLikeOpenQuestion(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" || isURLOnlyLine(body) {
		return false
	}
	if m := questionPrefix.FindStringSubmatch(body); len(m) == 2 {
		inner := strings.TrimSpace(m[1])
		return utf8.RuneCountInString(inner) >= 4
	}
	if !strings.HasSuffix(body, "?") && !strings.HasSuffix(body, "？") {
		return false
	}
	core := strings.TrimSpace(strings.TrimRight(body, "?？ \t"))
	if core == "" {
		return false
	}
	n := utf8.RuneCountInString(body)
	if n < 8 {
		return false
	}
	// Reject one-token tags like "opencli?" while keeping real questions.
	if !strings.ContainsAny(core, " \t") && !containsCJK(core) && utf8.RuneCountInString(core) < 16 {
		return false
	}
	return true
}

func normalizeExtractedQuestionBody(raw string) string {
	body := strings.TrimSpace(raw)
	if m := questionPrefix.FindStringSubmatch(body); len(m) == 2 {
		body = strings.TrimSpace(m[1])
	}
	// Keep the terminal ? / ？ so the stored wording stays natural.
	return strings.TrimSpace(body)
}

// ExtractQuestions deterministically extracts and batch-deduplicates questions.
func ExtractQuestions(lines []string) []ExtractedQuestion {
	found := make(map[string]ExtractedQuestion)
	order := make([]string, 0)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		stripped := stripQuestionDecorations(line)
		if !looksLikeOpenQuestion(stripped) {
			continue
		}
		body := normalizeExtractedQuestionBody(stripped)
		if body == "" || len([]rune(body)) > 2000 {
			continue
		}
		canonical := CanonicalizeQuestionBody(body)
		if canonical == "" {
			continue
		}
		if _, exists := found[canonical]; exists {
			continue
		}
		found[canonical] = ExtractedQuestion{Body: body, Canonical: canonical, SourceLine: line}
		order = append(order, canonical)
	}
	out := make([]ExtractedQuestion, 0, len(order))
	for _, canonical := range order {
		out = append(out, found[canonical])
	}
	return out
}

type QuestionIngestOptions struct {
	Lines            []string
	SourceDocumentID string
	SourceFile       string
	SourceCommit     string
}

type QuestionIngestResult struct {
	Found    int
	Inserted []port.Question
	Existing []port.Question
}

// IngestQuestionLines extracts questions from source lines and upserts them.
func (s *Service) IngestQuestionLines(ctx context.Context, opts QuestionIngestOptions) (QuestionIngestResult, error) {
	release, err := s.beginMutation()
	if err != nil {
		return QuestionIngestResult{}, err
	}
	defer release()

	store, err := s.questions()
	if err != nil {
		return QuestionIngestResult{}, err
	}

	extracted := ExtractQuestions(opts.Lines)
	inputs := make([]port.QuestionInsert, 0, len(extracted))
	for _, item := range extracted {
		id, err := s.ids.NewDocumentID()
		if err != nil {
			return QuestionIngestResult{}, err
		}
		inputs = append(inputs, port.QuestionInsert{
			ID:               string(id),
			Body:             item.Body,
			CanonicalBody:    item.Canonical,
			SourceDocumentID: strings.TrimSpace(opts.SourceDocumentID),
			SourceFile:       strings.TrimSpace(opts.SourceFile),
			SourceLine:       item.SourceLine,
			SourceCommit:     strings.TrimSpace(opts.SourceCommit),
			CreatedAt:        s.clock.Now(),
		})
	}
	results, err := store.Ingest(ctx, inputs)
	if err != nil {
		return QuestionIngestResult{}, err
	}
	out := QuestionIngestResult{Found: len(extracted)}
	for _, result := range results {
		if result.Inserted {
			out.Inserted = append(out.Inserted, result.Question)
		} else {
			out.Existing = append(out.Existing, result.Question)
		}
	}
	return out, nil
}

// IngestQuestionDocument reads an indexed Markdown document and ingests questions.
// Same safety rule as resources: never read arbitrary unindexed paths.
func (s *Service) IngestQuestionDocument(ctx context.Context, sourceDocumentID, sourceFile, sourceCommit string) (QuestionIngestResult, error) {
	var documentID, absolutePath string
	if selector := strings.TrimSpace(sourceDocumentID); selector != "" {
		document, path, err := s.ResolveDocument(ctx, selector)
		if err != nil {
			return QuestionIngestResult{}, err
		}
		documentID, absolutePath = string(document.ID), path
	} else {
		candidate, err := filepath.Abs(strings.TrimSpace(sourceFile))
		if err != nil || strings.TrimSpace(sourceFile) == "" {
			return QuestionIngestResult{}, fmt.Errorf("source_document_id or indexed source_file is required")
		}
		candidate = filepath.Clean(candidate)
		if resolved, resolveErr := filepath.EvalSymlinks(candidate); resolveErr == nil {
			candidate = resolved
		}
		paths, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return QuestionIngestResult{}, err
		}
		for _, summary := range paths {
			relative, relErr := filepath.Rel(summary.Path.Root, candidate)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			documents, listErr := s.store.DocumentsForPath(ctx, summary.Path.ID)
			if listErr != nil {
				return QuestionIngestResult{}, listErr
			}
			for _, document := range documents {
				if filepath.Clean(filepath.FromSlash(document.Location.RelativePath)) == filepath.Clean(relative) {
					documentID, absolutePath = string(document.ID), candidate
					break
				}
			}
			if documentID != "" {
				break
			}
		}
		if documentID == "" {
			return QuestionIngestResult{}, fmt.Errorf("source_file is not an indexed membox document: %s", candidate)
		}
	}
	body, err := s.reader.Read(ctx, absolutePath)
	if err != nil {
		return QuestionIngestResult{}, err
	}
	return s.IngestQuestionLines(ctx, QuestionIngestOptions{
		Lines: strings.Split(string(body), "\n"), SourceDocumentID: documentID,
		SourceFile: absolutePath, SourceCommit: sourceCommit,
	})
}

// ListQuestionsBySource returns questions previously ingested from a source file.
func (s *Service) ListQuestionsBySource(ctx context.Context, sourceFile string, limit int) ([]port.Question, error) {
	store, err := s.questions()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	return store.List(ctx, port.QuestionListQuery{SourceFile: strings.TrimSpace(sourceFile), Limit: limit})
}
