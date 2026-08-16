package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"membox/internal/application/port"
)

// ReviewGrade is one rating a user gives a card in the review feed.
type ReviewGrade string

const (
	GradeAgain ReviewGrade = "again" // forgot — reschedule now
	GradeHard  ReviewGrade = "hard"  // fuzzy — shorten interval
	GradeGood  ReviewGrade = "good"  // remembered — lengthen interval
)

// ReviewCardView is the API shape for one review card: note content plus
// source identity and schedule state.
type ReviewCardView struct {
	NoteID           string  `json:"note_id"`
	Kind             string  `json:"kind"`
	Highlight        bool    `json:"highlight"`
	Underline        bool    `json:"underline"`
	Strikethrough    bool    `json:"strikethrough"`
	Quote            string  `json:"quote"`
	Body             string  `json:"body"`
	SourceURL        string  `json:"source_url"`
	SourceTitle      string  `json:"source_title"`
	TargetID         string  `json:"target_id"`
	TargetTitle      string  `json:"target_title"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
	DueAt            int64   `json:"due_at"`
	IntervalDays     int64   `json:"interval_days"`
	Ease             float64 `json:"ease"`
	Reps             int64   `json:"reps"`
	Lapses           int64   `json:"lapses"`
	LastReviewedAt   int64   `json:"last_reviewed_at"`
	RepliesCount     int     `json:"replies_count"`
	IsNew            bool    `json:"is_new"`
	IsDue            bool    `json:"is_due"`
	// Position is this card's 1-based index in the FULL mixed queue (new →
	// due → aging), so the feed shows where the current page sits in 217.
	Position int `json:"position"`
}

// ReviewStats is the queue headline the feed header shows.
type ReviewStats struct {
	New  int `json:"new"`
	Due  int `json:"due"`
	Seen int `json:"seen"`
	All  int `json:"all"`
}

// ReviewQueue is the response of GET /api/review/queue.
type ReviewQueue struct {
	Cards []ReviewCardView `json:"cards"`
	Stats ReviewStats      `json:"stats"`
}

// ListReviewQueue returns the review feed: newest-first cards, then mixed by
// due/aging weight so the user browses old notes naturally without a hard
// scheduler gate. limit caps the returned batch.
func (s *Service) ListReviewQueue(ctx context.Context, limit, offset int) (ReviewQueue, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	cards, err := s.store.ListReviewCards(ctx)
	if err != nil {
		return ReviewQueue{}, err
	}
	if len(cards) == 0 {
		return ReviewQueue{Stats: ReviewStats{All: 0}}, nil
	}

	now := s.clock.Now().UnixMilli()
	var stats ReviewStats
	stats.All = len(cards)
	views := make([]ReviewCardView, 0, len(cards))
	byID := make(map[string]*port.ReviewCard, len(cards))
	for i := range cards {
		card := cards[i]
		byID[card.NoteDocumentID] = &card
		due := card.Schedule.DueAt
		isNew := due == 0
		isDue := !isNew && due <= now
		if isNew {
			stats.New++
		} else if isDue {
			stats.Due++
		} else {
			stats.Seen++
		}
	}

	// Mix: (1) never-rated cards newest first, (2) due cards by urgency, then
	// (3) seen cards by recency with a light random wobble so browsing feels
	// alive instead of deterministic.
	order := make([]string, 0, len(cards))
	var dueIDs, seenIDs []string
	for _, card := range cards {
		if card.Schedule.DueAt == 0 {
			order = append(order, card.NoteDocumentID)
		} else if card.Schedule.DueAt <= now {
			dueIDs = append(dueIDs, card.NoteDocumentID)
		} else {
			seenIDs = append(seenIDs, card.NoteDocumentID)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byID[order[i]].CreatedAt.After(byID[order[j]].CreatedAt)
	})
	sort.Slice(dueIDs, func(i, j int) bool {
		return byID[dueIDs[i]].Schedule.DueAt < byID[dueIDs[j]].Schedule.DueAt
	})
	// Recent first, wobble within each recency bucket (deterministic seed per
	// card id so the order is stable across pagination refreshes).
	sort.SliceStable(seenIDs, func(i, j int) bool {
		a, b := byID[seenIDs[i]], byID[seenIDs[j]]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return hashString(seenIDs[i]) < hashString(seenIDs[j])
	})
	order = append(order, dueIDs...)
	order = append(order, seenIDs...)

	for index, id := range order {
		if index < offset {
			continue
		}
		if index >= offset+limit {
			break
		}
		card := byID[id]
		view, viewErr := s.reviewCardView(ctx, *card)
		if viewErr != nil {
			continue // skip notes whose files vanished
		}
		view.IsNew = card.Schedule.DueAt == 0
		view.IsDue = card.Schedule.DueAt != 0 && card.Schedule.DueAt <= now
		view.Position = index + 1
		views = append(views, view)
	}
	return ReviewQueue{Cards: views, Stats: stats}, nil
}

// RateReviewCard applies one spaced-review grade and returns the card's new
// schedule state.
func (s *Service) RateReviewCard(ctx context.Context, noteSelector string, grade ReviewGrade) (port.CardSchedule, error) {
	note, _, err := s.ResolveDocument(ctx, noteSelector)
	if err != nil {
		return port.CardSchedule{}, err
	}
	if _, ok, err := s.store.GetAnnotationNote(ctx, note.ID); err != nil {
		return port.CardSchedule{}, err
	} else if !ok {
		return port.CardSchedule{}, fmt.Errorf("document %s is not an annotation note", note.ID)
	}

	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return port.CardSchedule{}, lockErr
	}
	defer release()

	// Re-read the schedule inside the mutation lock so two tabs rating the
	// same card at once converge instead of losing one update.
	sched, err := s.cardSchedule(ctx, string(note.ID))
	if err != nil {
		return port.CardSchedule{}, err
	}
	now := s.clock.Now()
	interval := sched.IntervalDays
	ease := sched.Ease
	if ease <= 0 {
		ease = 2.5
	}
	switch grade {
	case GradeAgain:
		interval = 0
		ease = ease - 0.4
		if ease < 1.3 {
			ease = 1.3
		}
		sched.Lapses++
	case GradeHard:
		if interval <= 0 {
			interval = 1
		} else {
			interval = max64(1, interval/2)
		}
		ease = ease - 0.2
		if ease < 1.3 {
			ease = 1.3
		}
	case GradeGood:
		if interval <= 0 {
			interval = 1
		} else {
			interval = max64(1, int64(float64(interval)*ease))
		}
		ease = ease + 0.1
		if ease > 3.5 {
			ease = 3.5
		}
	default:
		return port.CardSchedule{}, errors.New("grade must be again, hard, or good")
	}
	sched.IntervalDays = interval
	sched.Ease = ease
	sched.Reps++
	sched.LastReviewedAt = now.UnixMilli()
	sched.DueAt = now.Add(time.Duration(interval) * 24 * time.Hour).UnixMilli()
	if interval <= 0 {
		sched.DueAt = now.UnixMilli() // again → due immediately
	}
	if err := s.store.SaveCardSchedule(ctx, sched); err != nil {
		return port.CardSchedule{}, err
	}
	return sched, nil
}

// SummarizeReviewCard runs the local mmd model over the note body and stores
// the ≤140-char summary in the note document's index metadata. Returns the
// generated summary plus the model label that produced it.
func (s *Service) SummarizeReviewCard(ctx context.Context, noteSelector string, complete func(ctx context.Context, prompt string) (string, error)) (string, string, error) {
	body, err := s.ReadDocument(ctx, noteSelector)
	if err != nil {
		return "", "", err
	}
	text := string(body)
	_, rest := splitFrontMatter(text)
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", errors.New("note is empty")
	}
	if len([]rune(rest)) > 4000 {
		rest = string([]rune(rest)[:4000])
	}
	if complete == nil {
		return "", "", errors.New("local model (mmd) is not configured")
	}
	prompt := "你是笔记整理助手。把下面的笔记内容压缩成一段 140 字以内的中文总结，保留核心观点，直接输出总结本身，不要前缀不要解释。\n\n笔记内容：\n" + rest + "\n\n140字以内的总结："
	generated, err := complete(ctx, prompt)
	if err != nil {
		return "", "", err
	}
	summary := strings.TrimSpace(generated)
	if summary == "" {
		return "", "", errors.New("local model returned an empty summary")
	}
	// Store it as the note document's summary (TUI preview + index metadata).
	if _, err := s.SetDocumentSummary(ctx, noteSelector, summary); err != nil {
		return "", "", err
	}
	return summary, "local qwen3:14b", nil
}

// ReplyToReviewCard appends a dated reply block to the note file and bumps
// annotation_notes.updated_at so the feed shows the card as fresh.
func (s *Service) ReplyToReviewCard(ctx context.Context, noteSelector, reply string) (ReviewCardView, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ReviewCardView{}, errors.New("reply is empty")
	}
	body, err := s.ReadDocument(ctx, noteSelector)
	if err != nil {
		return ReviewCardView{}, err
	}
	text := strings.TrimRight(string(body), "\n")
	now := s.clock.Now()
	stamp := now.Format("2006-01-02 15:04")
	var next string
	if strings.TrimSpace(text) == "" {
		next = fmt.Sprintf("--- 跟帖 (%s) ---\n%s\n", stamp, reply)
	} else {
		next = text + "\n\n--- 跟帖 (" + stamp + ") ---\n" + reply + "\n"
	}
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return ReviewCardView{}, lockErr
	}
	defer release()

	note, _, err := s.ResolveDocument(ctx, noteSelector)
	if err != nil {
		return ReviewCardView{}, err
	}
	record, ok, err := s.store.GetAnnotationNote(ctx, note.ID)
	if err != nil {
		return ReviewCardView{}, err
	}
	if !ok {
		return ReviewCardView{}, fmt.Errorf("document %s is not an annotation note", note.ID)
	}
	if _, err := s.SyncDocument(ctx, noteSelector, next); err != nil {
		return ReviewCardView{}, err
	}
	record.UpdatedAt = now
	if err := s.store.UpsertAnnotationNote(ctx, record); err != nil {
		return ReviewCardView{}, err
	}
	// Refresh the card view with the reply applied.
	cards, err := s.store.ListReviewCards(ctx)
	if err != nil {
		return ReviewCardView{}, err
	}
	for i := range cards {
		if cards[i].NoteDocumentID == string(note.ID) {
			return s.reviewCardView(ctx, cards[i])
		}
	}
	return ReviewCardView{}, fmt.Errorf("review card %s not found after reply", note.ID)
}

func (s *Service) cardSchedule(ctx context.Context, noteID string) (port.CardSchedule, error) {
	cards, err := s.store.ListReviewCards(ctx)
	if err != nil {
		return port.CardSchedule{}, err
	}
	for i := range cards {
		if cards[i].NoteDocumentID == noteID {
			return cards[i].Schedule, nil
		}
	}
	return port.CardSchedule{NoteDocumentID: noteID, Ease: 2.5}, nil
}

// reviewCardView assembles the API view: note body (front matter stripped),
// quote line, source identity, and reply count.
func (s *Service) reviewCardView(ctx context.Context, card port.ReviewCard) (ReviewCardView, error) {
	body, err := s.ReadDocument(ctx, card.NoteDocumentID)
	if err != nil {
		return ReviewCardView{}, err
	}
	text := string(body)
	fm, rest := splitFrontMatter(text)
	quote := firstQuote(rest)
	sourceURL, sourceTitle := frontMatterFields(fm)

	view := ReviewCardView{
		NoteID:        card.NoteDocumentID,
		Kind:          card.Kind,
		Highlight:     card.Highlight,
		Underline:     card.Underline,
		Strikethrough: card.Strikethrough,
		Quote:         quote,
		Body:          strings.TrimSpace(rest),
		SourceURL:     sourceURL,
		SourceTitle:   sourceTitle,
		TargetID:      card.TargetDocumentID,
		CreatedAt:     card.CreatedAt.UnixMilli(),
		UpdatedAt:     card.UpdatedAt.UnixMilli(),
		DueAt:         card.Schedule.DueAt,
		IntervalDays:  card.Schedule.IntervalDays,
		Ease:          card.Schedule.Ease,
		Reps:          card.Schedule.Reps,
		Lapses:        card.Schedule.Lapses,
		LastReviewedAt: card.Schedule.LastReviewedAt,
		RepliesCount:  countReplies(rest),
	}
	if target, _, targetErr := s.ResolveDocument(ctx, card.TargetDocumentID); targetErr == nil && target != nil {
		view.TargetTitle = target.Index.Title
	}
	return view, nil
}

// splitFrontMatter returns the YAML block (with --- fences) and the rest.
func splitFrontMatter(text string) (front, rest string) {
	t := strings.TrimLeft(text, "\ufeff \t\r\n")
	if !strings.HasPrefix(t, "---") {
		return "", text
	}
	body := t[3:]
	if end := strings.Index(body, "\n---"); end >= 0 {
		return t[:3] + body[:end] + "---", body[end+4:]
	}
	return "", text
}

// frontMatterFields extracts source_url and source_title (quoted or bare).
func frontMatterFields(front string) (url, title string) {
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "source_url:"):
			url = unquoteYAML(strings.TrimSpace(strings.TrimPrefix(line, "source_url:")))
		case strings.HasPrefix(line, "source_title:"):
			title = unquoteYAML(strings.TrimSpace(strings.TrimPrefix(line, "source_title:")))
		}
	}
	return url, title
}

func unquoteYAML(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}

// firstQuote returns the first blockquote line in the note body (the original
// selected excerpt), or the first non-empty line as a fallback.
func firstQuote(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, ">") {
			return strings.TrimSpace(strings.TrimPrefix(trim, ">"))
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim != "" {
			return trim
		}
	}
	return ""
}

// countReplies counts the "--- 跟帖" separators in a note body.
func countReplies(text string) int {
	return strings.Count(text, "--- 跟帖 (")
}

// hashString is a tiny stable hash used to wobble the aging bucket order.
func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
