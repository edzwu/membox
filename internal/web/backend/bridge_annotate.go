package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// Selection notes are mirrored as Miru annotations on the page clip they
// belong to. The note document stays the entity; the annotation is a
// projection the reader renders natively (highlight + margin note).

const annotContextRunes = 32

// upsertClipAnnotation anchors an excerpt inside the page clip's Markdown and
// writes (or updates) the matching Miru annotation in its sidecar. Failures
// are non-fatal: the note document and graph link already exist. Returns
// whether an annotation was actually projected.
func (s *Server) upsertClipAnnotation(ctx context.Context, pageID, refID, excerpt, note string) (bool, error) {
	excerpt = strings.TrimSpace(excerpt)
	if pageID == "" || excerpt == "" {
		return false, nil
	}
	body, err := s.service.ReadDocument(ctx, pageID)
	if err != nil {
		return false, nil
	}
	canonical := markdownToCanonicalText(string(body))
	start, end, ok := findExcerptOffsets(canonical, excerpt)
	if !ok {
		// Excerpt not found in this document version — skip silently. The
		// note document still exists; a later re-save can retry.
		return false, nil
	}
	runes := []rune(canonical)
	prefixStart := start - annotContextRunes
	if prefixStart < 0 {
		prefixStart = 0
	}
	suffixEnd := end + annotContextRunes
	if suffixEnd > len(runes) {
		suffixEnd = len(runes)
	}

	sidecar := map[string]any{}
	if existing, err := s.service.GetAnnotations(ctx, pageID); err == nil && strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &sidecar)
	}
	annotations := []map[string]any{}
	if raw, ok := sidecar["annotations"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				annotations = append(annotations, m)
			}
		}
	}

	// Idempotent: same excerpt text identifies the same projection.
	var entry map[string]any
	for _, candidate := range annotations {
		if exact, _ := candidate["exact"].(string); exact == excerpt {
			entry = candidate
			break
		}
	}
	noteValue := any(nil)
	if strings.TrimSpace(note) != "" {
		noteValue = note
	}
	desired := map[string]any{
		"start": start, "end": end, "exact": excerpt,
		"prefix": string(runes[prefixStart:start]), "suffix": string(runes[end:suffixEnd]),
		"highlight": true, "underline": false, "strikethrough": false,
		"note": noteValue,
	}
	if refID != "" {
		desired["ref"] = refID
	}
	// Skip the write when the stored projection already matches, so repeated
	// scans (Ctrl+R auto-repair) never churn the sidecar.
	if entry != nil && sameJSON(entry, desired) {
		return true, nil
	}
	if entry == nil {
		annotations = append(annotations, desired)
	} else {
		for k, v := range desired {
			entry[k] = v
		}
	}

	sidecar["format"] = "miru-annotations"
	sidecar["version"] = 2
	sidecar["annotations"] = annotations
	// Miru verifies the sidecar against the loaded Markdown: hash must match
	// and sourceLength is compared to the JS string length (UTF-16 units).
	sum := sha256.Sum256(body)
	sidecar["sourceHash"] = "sha256:" + hex.EncodeToString(sum[:])
	sidecar["sourceLength"] = jsStringLength(string(body))
	if _, hasTitle := sidecar["title"]; !hasTitle {
		sidecar["title"] = titleFromBody(string(body))
	}

	out, err := json.Marshal(sidecar)
	if err != nil {
		return false, err
	}
	if _, err := s.service.SaveAnnotations(ctx, pageID, string(out)); err != nil {
		return false, err
	}
	return true, nil
}

// backfillPageAnnotations mirrors every existing selection note for a URL
// onto a page clip (covers "notes saved before the page" and migration of
// pre-existing *-note.md files). Returns how many notes were projected.
func (s *Server) backfillPageAnnotations(ctx context.Context, pageID, sourceURL string) int {
	records, err := s.service.ListClipsBySourceURL(ctx, sourceURL, true)
	if err != nil {
		return 0
	}
	projected := 0
	for _, rec := range records {
		body, err := s.service.ReadDocument(ctx, string(rec.DocumentID))
		if err != nil {
			continue
		}
		excerpt, note, mode := parseClipBody(string(body))
		if mode != "selection" || strings.TrimSpace(excerpt) == "" {
			continue
		}
		if ok, _ := s.upsertClipAnnotation(ctx, pageID, string(rec.DocumentID), excerpt, note); ok {
			projected++
		}
	}
	return projected
}

// ProjectExistingAnnotations rebuilds Miru annotation projections for every
// stored page clip from its selection notes. One-shot migration for data
// created before projections existed; safe to re-run (idempotent upserts).
func (s *Server) ProjectExistingAnnotations(ctx context.Context) (pages, projected int, err error) {
	clipPages, err := s.service.ListDocumentSources(ctx, "page")
	if err != nil {
		return 0, 0, err
	}
	for _, page := range clipPages {
		if strings.TrimSpace(page.SourceURL) == "" {
			continue
		}
		pages++
		projected += s.backfillPageAnnotations(ctx, string(page.DocumentID), page.SourceURL)
	}
	return pages, projected, nil
}

// ---------------------------------------------------------------------------
// Markdown → approximate rendered text, and excerpt search.
// The frontend re-anchors by text quote (resolveAnnotationRange), so these
// offsets only need to be good hints, not pixel-perfect.
// ---------------------------------------------------------------------------

var (
	reFence      = regexp.MustCompile("^\\s*(```|~~~)")
	reHeading    = regexp.MustCompile(`^#{1,6}\s+`)
	reQuote      = regexp.MustCompile(`^>\s?`)
	reListMarker = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	reImage      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reBold       = regexp.MustCompile(`\*\*|__`)
	reEmph       = regexp.MustCompile(`\*([^*\n]+)\*|_([^_\n]+)_`)
	reCode       = regexp.MustCompile("`([^`\n]*)`")
	reSpaces     = regexp.MustCompile(`[ \t\x{00a0}]+`)
)

// markdownToCanonicalText strips Markdown syntax, keeping block structure as
// single newlines, approximating the text Miru extracts from the rendered
// article.
func markdownToCanonicalText(body string) string {
	text := stripFrontMatter(body)
	var out []string
	inCode := false
	for _, line := range strings.Split(text, "\n") {
		if reFence.MatchString(line) {
			inCode = !inCode
			continue
		}
		if inCode {
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
			continue
		}
		l := reHeading.ReplaceAllString(line, "")
		l = reQuote.ReplaceAllString(l, "")
		l = reListMarker.ReplaceAllString(l, "")
		l = cleanInlineMarkdown(l)
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func stripFrontMatter(body string) string {
	trim := strings.TrimLeft(body, "\ufeff \t\r\n")
	if !strings.HasPrefix(trim, "---") {
		return body
	}
	rest := trim[3:]
	if end := strings.Index(rest, "\n---"); end >= 0 {
		return rest[end+4:]
	}
	return body
}

func cleanInlineMarkdown(s string) string {
	s = reImage.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = reBold.ReplaceAllString(s, "")
	s = reEmph.ReplaceAllString(s, "$1$2")
	s = reCode.ReplaceAllString(s, "$1")
	s = reSpaces.ReplaceAllString(s, " ")
	return s
}

// collapseRunes collapses whitespace runs to single spaces and records, for
// each output rune, the rune index it came from in the input.
func collapseRunes(s string) (string, []int) {
	runes := []rune(s)
	var b strings.Builder
	mapping := make([]int, 0, len(runes))
	prevSpace := false
	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == '\u00a0' {
			if !prevSpace && b.Len() > 0 {
				b.WriteRune(' ')
				mapping = append(mapping, i)
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		mapping = append(mapping, i)
		prevSpace = false
	}
	return b.String(), mapping
}

// findExcerptOffsets locates the excerpt in canonical text with flexible
// whitespace, returning rune offsets into the un-collapsed canonical text.
func findExcerptOffsets(canonical, excerpt string) (int, int, bool) {
	normC, mapping := collapseRunes(canonical)
	normE, _ := collapseRunes(excerpt)
	normE = strings.TrimSpace(normE)
	if normE == "" {
		return 0, 0, false
	}
	idx := strings.Index(normC, normE)
	if idx < 0 {
		return 0, 0, false
	}
	startNorm := len([]rune(normC[:idx]))
	endNorm := startNorm + len([]rune(normE)) - 1
	if startNorm >= len(mapping) || endNorm >= len(mapping) {
		return 0, 0, false
	}
	start := mapping[startNorm]
	end := mapping[endNorm] + 1
	if end > len([]rune(canonical)) {
		end = len([]rune(canonical))
	}
	return start, end, true
}

// jsStringLength counts UTF-16 code units the way JavaScript's String.length
// does; Miru verifies sourceLength against markdown.length.
// sameJSON compares two annotation maps by their canonical JSON form
// (json.Marshal sorts map keys).
func sameJSON(a, b map[string]any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

func jsStringLength(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
