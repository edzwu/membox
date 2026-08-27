package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"membox/internal/domain/catalog"
)

// NoteTitleNamer suggests a short human title when a quick note has no H1.
// The TUI wires this to mmd's local completer; tests inject a stub.
type NoteTitleNamer interface {
	NameNote(ctx context.Context, body string) (string, error)
}

// FinalizeQuickNoteResult is the outcome of naming a scratch note after the
// editor closes (ctrl+n → vim → :wq).
type FinalizeQuickNoteResult struct {
	Document    *catalog.Document
	Path        string
	Title       string
	Filename    string
	Deleted     bool
	UsedLLM     bool
	NeedsNaming bool
}

// FinalizeQuickNote renames a scratch note from its body:
//  1. empty / whitespace-only → move to trash
//  2. first ATX H1 → slug(filename) + catalog title
//  3. otherwise → local LLM title (namer), then slug; on failure, time stamp
func (s *Service) FinalizeQuickNote(ctx context.Context, selector string, namer NoteTitleNamer) (FinalizeQuickNoteResult, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return FinalizeQuickNoteResult{}, errors.New("document selector is required")
	}
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	if document.Status != catalog.DocumentActive {
		return FinalizeQuickNoteResult{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	bodyBytes, err := s.ReadDocument(ctx, string(document.ID))
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	body := string(bodyBytes)
	if strings.TrimSpace(stripMarkdownFrontMatter(body)) == "" {
		if _, _, err := s.TrashDocumentFile(ctx, string(document.ID)); err != nil {
			return FinalizeQuickNoteResult{}, err
		}
		return FinalizeQuickNoteResult{
			Document: document,
			Path:     absolute,
			Filename: filepath.Base(absolute),
			Deleted:  true,
		}, nil
	}

	title := strings.TrimSpace(ExtractFirstMarkdownH1(body))
	usedLLM := false
	if title == "" && namer != nil {
		suggested, nameErr := namer.NameNote(ctx, body)
		if nameErr != nil {
			return FinalizeQuickNoteResult{}, fmt.Errorf("name note via LLM: %w", nameErr)
		}
		title = sanitizeSuggestedNoteTitle(suggested)
		usedLLM = title != ""
	}
	if title == "" {
		title = fallbackQuickNoteTitle(s.clock.Now())
	}

	slug := noteSlug(title)
	if slug == "" {
		slug = noteSlug(fallbackQuickNoteTitle(s.clock.Now()))
	}
	if slug == "" {
		slug = "note"
	}
	desired := slug + ".md"

	// Prefer keeping the current name when it already matches the slug
	// (including collision suffixes like foo-2.md for title "Foo").
	currentBase := filepath.Base(absolute)
	newFilename := desired
	if currentBase != desired && !isSlugCollisionName(currentBase, slug) {
		root := filepath.Dir(absolute)
		tracked := map[string]bool{}
		if docs, docsErr := s.store.DocumentsForPath(ctx, document.Location.PathID); docsErr == nil {
			for _, d := range docs {
				// The note being renamed may keep occupying its current relative
				// path until Move finishes — exclude it from the collision set.
				if d.ID == document.ID {
					continue
				}
				tracked[filepath.ToSlash(d.Location.RelativePath)] = true
			}
		}
		targetAbs, pathErr := s.availableNotePath(root, desired, tracked)
		if pathErr != nil {
			return FinalizeQuickNoteResult{}, pathErr
		}
		newFilename = filepath.Base(targetAbs)
	} else {
		newFilename = currentBase
	}

	renamed, err := s.RenameDocument(ctx, string(document.ID), newFilename, title)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	resolved, path, err := s.ResolveDocument(ctx, string(renamed.DocumentID))
	if err != nil {
		return FinalizeQuickNoteResult{
			Path:     renamed.Path,
			Title:    renamed.Title,
			Filename: newFilename,
			UsedLLM:  usedLLM,
		}, nil
	}
	return FinalizeQuickNoteResult{
		Document: resolved,
		Path:     path,
		Title:    renamed.Title,
		Filename: newFilename,
		UsedLLM:  usedLLM,
	}, nil
}

// ExtractFirstMarkdownH1 returns the first ATX heading level-1 text outside
// YAML front matter and fenced code blocks. Empty when none.
func ExtractFirstMarkdownH1(body string) string {
	lines := strings.Split(body, "\n")
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if i == 0 && trim == "---" {
			continue
		}
		if inFrontmatter {
			if trim == "---" || trim == "..." {
				inFrontmatter = false
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			mark := trim[:3]
			if !inFence {
				inFence, fenceMarker = true, mark
			} else if strings.HasPrefix(trim, fenceMarker) {
				inFence, fenceMarker = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trim, "# ") && !strings.HasPrefix(trim, "##") {
			return strings.TrimSpace(strings.TrimPrefix(trim, "# "))
		}
	}
	return ""
}

func stripMarkdownFrontMatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for i := 1; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if trim == "---" || trim == "..." {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return body
}

func sanitizeSuggestedNoteTitle(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	// Models sometimes wrap answers in quotes or prefix "Title:".
	text = strings.Trim(text, "`\"'")
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	for _, prefix := range []string{"title:", "filename:", "name:", "建议标题:", "标题:"} {
		if strings.HasPrefix(lower, prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			text = strings.Trim(text, "`\"'")
			break
		}
	}
	// Keep a short display title; slug handles filesystem safety.
	fields := strings.Fields(text)
	if len(fields) > 12 {
		fields = fields[:12]
	}
	text = strings.Join(fields, " ")
	if utf8.RuneCountInString(text) > 80 {
		runes := []rune(text)
		text = string(runes[:80])
	}
	// Reject pure punctuation / empty after trim.
	if noteSlug(text) == "" {
		// Allow CJK-only titles (noteSlug keeps letters including CJK).
		hasLetter := false
		for _, r := range text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				hasLetter = true
				break
			}
		}
		if !hasLetter {
			return ""
		}
	}
	return strings.TrimSpace(text)
}

func fallbackQuickNoteTitle(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return "note " + now.Format("2006-01-02 1504")
}

func isSlugCollisionName(filename, slug string) bool {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == slug {
		return true
	}
	// slug-2, slug-3, …
	if !strings.HasPrefix(base, slug+"-") {
		return false
	}
	suffix := strings.TrimPrefix(base, slug+"-")
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// QuickNoteTitlePrompt builds the mmd completion prompt for untitled notes.
func QuickNoteTitlePrompt(body string) string {
	const maxRunes = 1800
	sample := strings.TrimSpace(stripMarkdownFrontMatter(body))
	if utf8.RuneCountInString(sample) > maxRunes {
		runes := []rune(sample)
		sample = string(runes[:maxRunes])
	}
	return strings.TrimSpace(`You name a personal Markdown scratch note.
Reply with ONLY a short title (3–10 words). No quotes, no punctuation wrapper, no explanation, no filename extension.

Note body:
---
` + sample + `
---
Title:`)
}
