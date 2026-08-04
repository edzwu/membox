package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"membox/internal/application"
	"membox/internal/application/port"
)

// Selection-note Markdown documents are linked to the page clips they
// annotate. Miru's sidecar shape is now only an on-read rendering DTO.

const annotContextRunes = 32

// upsertClipAnnotationRelation anchors an excerpt inside the page clip and
// stores the note UUID relationship plus anchor hints. Note content remains in
// Markdown. Failures are non-fatal to browser ingest.
func (s *Server) upsertClipAnnotationRelation(ctx context.Context, pageID, refID, excerpt string) (bool, error) {
	s.annotationMu.Lock()
	defer s.annotationMu.Unlock()
	// Normalize the excerpt exactly like the page text is normalized: note
	// bodies carry Turndown artifacts (****, backticks, \_ escapes) that must
	// disappear both for matching here and for Miru's text-quote re-anchoring.
	excerpt = strings.TrimSpace(cleanInlineMarkdown(excerpt))
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
	// Store the page-side span as `exact` (rather than the note's excerpt):
	// it reflects the page text Miru re-anchors against, so the annotation
	// survives even when the note's blockquote carried different spacing.
	excerpt = string(runes[start:end])
	prefixStart := start - annotContextRunes
	if prefixStart < 0 {
		prefixStart = 0
	}
	suffixEnd := end + annotContextRunes
	if suffixEnd > len(runes) {
		suffixEnd = len(runes)
	}

	if refID == "" {
		return false, nil
	}
	// Persist only the UUID relation and anchoring hints. The excerpt and note
	// prose remain authoritative in refID's Markdown file; a Miru sidecar is
	// assembled from the two sources only when the page is read.
	_, err = s.service.SaveAnnotationNote(ctx, application.SaveAnnotationNoteOptions{
		TargetSelector: pageID,
		NoteSelector:   refID,
		// Browser offsets use UTF-16 code units, not Go rune indexes.
		Start:     jsStringLength(string(runes[:start])),
		Prefix:    string(runes[prefixStart:start]),
		Suffix:    string(runes[end:suffixEnd]),
		Highlight: true,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// backfillPageAnnotations links every existing selection note for a URL to a
// page clip (covers "notes saved before the page" and automatic migration of
// pre-existing *-note.md files). Returns how many relations were established.
func (s *Server) backfillPageAnnotations(ctx context.Context, pageID, sourceURL string) int {
	records, err := s.service.ListClipsBySourceURL(ctx, sourceURL, true)
	if err != nil {
		return 0
	}
	projected := 0
	for _, rec := range records {
		if existing, ok, getErr := s.service.GetAnnotationNote(ctx, string(rec.DocumentID)); getErr == nil && ok && string(existing.TargetDocumentID) == pageID {
			continue
		}
		body, err := s.service.ReadDocument(ctx, string(rec.DocumentID))
		if err != nil {
			continue
		}
		excerpt, _, mode := parseClipBody(string(body))
		if mode != "selection" || strings.TrimSpace(excerpt) == "" {
			continue
		}
		if ok, _ := s.upsertClipAnnotationRelation(ctx, pageID, string(rec.DocumentID), excerpt); ok {
			projected++
		}
	}
	return projected
}

// migrateExistingAnnotationNotes automatically links selection notes created
// before normalized annotation relations existed. It is idempotent, skips
// already-linked notes, and never persists a Miru sidecar.
func (s *Server) migrateExistingAnnotationNotes(ctx context.Context) error {
	clipPages, err := s.service.ListDocumentSources(ctx, "page")
	if err != nil {
		return err
	}
	for _, page := range clipPages {
		if strings.TrimSpace(page.SourceURL) != "" {
			s.backfillPageAnnotations(ctx, string(page.DocumentID), page.SourceURL)
		}
	}
	return nil
}

type annotationAnchorPayload struct {
	Start         int    `json:"start"`
	End           int    `json:"end,omitempty"`
	Exact         string `json:"exact"`
	Prefix        string `json:"prefix,omitempty"`
	Suffix        string `json:"suffix,omitempty"`
	Highlight     bool   `json:"highlight"`
	Underline     bool   `json:"underline"`
	Strikethrough bool   `json:"strikethrough"`
	Note          any    `json:"note"`
	Ref           string `json:"ref,omitempty"`
}

type annotationProgressPayload struct {
	Y  int    `json:"y"`
	At string `json:"at"`
}

type annotationWritePayload struct {
	Format             string                     `json:"format"`
	Version            int                        `json:"version"`
	Annotations        []annotationAnchorPayload  `json:"annotations"`
	Progress           *annotationProgressPayload `json:"progress"`
	ReplaceAnnotations bool                       `json:"replaceAnnotations"`
}

// liveAnnotationSidecar is an on-read view. It combines Markdown note content
// with normalized UUID/anchor rows and never stores the resulting JSON.
func (s *Server) liveAnnotationSidecar(ctx context.Context, pageID string) (map[string]any, bool, error) {
	s.annotationMu.Lock()
	defer s.annotationMu.Unlock()
	body, err := s.service.ReadDocument(ctx, pageID)
	if err != nil {
		return nil, false, err
	}
	_, records, err := s.service.ListAnnotationNotes(ctx, pageID)
	if err != nil {
		return nil, false, err
	}
	annotations := make([]map[string]any, 0, len(records))
	refs := map[string]bool{}
	type anchorKey struct {
		exact string
		start int
	}
	anchors := map[anchorKey]bool{}
	for _, record := range records {
		noteBody, readErr := s.service.ReadDocument(ctx, string(record.NoteDocumentID))
		if readErr != nil {
			continue
		}
		exact, note, _ := parseClipBody(string(noteBody))
		exact = strings.TrimSpace(cleanInlineMarkdown(exact))
		if exact == "" {
			continue
		}
		entry := annotationMap(record, exact, note)
		annotations = append(annotations, entry)
		refs[string(record.NoteDocumentID)] = true
		anchors[anchorKey{exact: exact, start: record.Start}] = true
	}

	// Keep pre-migration sidecars readable. Once the browser next saves, these
	// entries are materialized as Markdown notes and the legacy blob is cleared.
	legacy := map[string]any{}
	if raw, legacyErr := s.service.GetAnnotations(ctx, pageID); legacyErr == nil && strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &legacy)
		if items, ok := legacy["annotations"].([]any); ok {
			for _, rawItem := range items {
				item, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				ref, _ := item["ref"].(string)
				exact, _ := item["exact"].(string)
				start := 0
				if value, ok := item["start"].(float64); ok {
					start = int(value)
				}
				if (ref != "" && refs[ref]) || (ref == "" && anchors[anchorKey{exact: exact, start: start}]) {
					continue
				}
				annotations = append(annotations, item)
			}
		}
	}

	var progress any
	if readState, ok, stateErr := s.service.GetDocumentReadState(ctx, pageID); stateErr != nil {
		return nil, false, stateErr
	} else if ok {
		progress = map[string]any{"y": readState.ProgressY, "at": readState.ProgressAt}
	} else if value, ok := legacy["progress"]; ok {
		progress = value
	}

	sum := sha256.Sum256(body)
	sidecar := map[string]any{
		"format":       "miru-annotations",
		"version":      2,
		"sourceHash":   "sha256:" + hex.EncodeToString(sum[:]),
		"sourceLength": jsStringLength(string(body)),
		"title":        titleFromBody(string(body)),
		"annotations":  annotations,
		"progress":     progress,
	}
	return sidecar, len(annotations) > 0 || progress != nil, nil
}

func annotationMap(record port.AnnotationNoteRecord, exact, note string) map[string]any {
	noteValue := any(nil)
	if strings.TrimSpace(note) != "" {
		noteValue = note
	}
	return map[string]any{
		"start": record.Start, "end": record.Start + jsStringLength(exact), "exact": exact,
		"prefix": record.Prefix, "suffix": record.Suffix,
		"highlight": record.Highlight, "underline": record.Underline, "strikethrough": record.Strikethrough,
		"note": noteValue, "ref": string(record.NoteDocumentID),
	}
}

// reconcileAnnotationNotes materializes every user annotation as a Markdown
// document. The request is a transient UI snapshot, not a stored sidecar.
func (s *Server) reconcileAnnotationNotes(ctx context.Context, pageID string, payload annotationWritePayload) ([]map[string]any, error) {
	s.annotationMu.Lock()
	defer s.annotationMu.Unlock()
	_, existing, err := s.service.ListAnnotationNotes(ctx, pageID)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]port.AnnotationNoteRecord, len(existing))
	for _, record := range existing {
		byRef[string(record.NoteDocumentID)] = record
	}
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(payload.Annotations))

	for _, anchor := range payload.Annotations {
		exact := strings.TrimSpace(anchor.Exact)
		if exact == "" {
			continue
		}
		note := ""
		if value, ok := anchor.Note.(string); ok {
			note = strings.TrimSpace(value)
		}
		ref := strings.TrimSpace(anchor.Ref)

		// A newly created Miru annotation has no ref until the first save. Match
		// it to the same anchor on subsequent saves rather than creating copies.
		if ref == "" {
			bestDistance := int(^uint(0) >> 1)
			for candidateRef, candidate := range byRef {
				if seen[candidateRef] {
					continue
				}
				body, readErr := s.service.ReadDocument(ctx, candidateRef)
				if readErr != nil {
					continue
				}
				candidateExact, _, _ := parseClipBody(string(body))
				if strings.TrimSpace(cleanInlineMarkdown(candidateExact)) != strings.TrimSpace(cleanInlineMarkdown(exact)) {
					continue
				}
				distance := candidate.Start - anchor.Start
				if distance < 0 {
					distance = -distance
				}
				if distance < bestDistance {
					bestDistance, ref = distance, candidateRef
				}
			}
		}

		body := selectionNoteMarkdown("", exact, note)
		if ref != "" {
			if current, readErr := s.service.ReadDocument(ctx, ref); readErr == nil {
				oldExact, oldNote, _ := parseClipBody(string(current))
				if strings.TrimSpace(cleanInlineMarkdown(oldExact)) == strings.TrimSpace(cleanInlineMarkdown(exact)) && strings.TrimSpace(oldNote) == note {
					body = "" // no content churn; update anchor metadata only
				} else {
					body = selectionNoteMarkdown(string(current), exact, note)
				}
			} else {
				ref = ""
			}
		}

		result, saveErr := s.service.SaveAnnotationNote(ctx, application.SaveAnnotationNoteOptions{
			TargetSelector: pageID, NoteSelector: ref, Title: annotationNoteTitle(exact), Body: body,
			Start: anchor.Start, Prefix: anchor.Prefix, Suffix: anchor.Suffix,
			Highlight: anchor.Highlight, Underline: anchor.Underline, Strikethrough: anchor.Strikethrough,
		})
		if saveErr != nil {
			return nil, saveErr
		}
		ref = string(result.Record.NoteDocumentID)
		seen[ref] = true
		out = append(out, annotationMap(result.Record, exact, note))
	}

	if payload.ReplaceAnnotations {
		for _, record := range existing {
			ref := string(record.NoteDocumentID)
			if !seen[ref] {
				if err := s.service.DeleteAnnotationNote(ctx, pageID, ref); err != nil {
					return nil, err
				}
			}
		}
	}
	if payload.Progress != nil {
		if err := s.service.SaveDocumentReadState(ctx, pageID, payload.Progress.Y, payload.Progress.At); err != nil {
			return nil, err
		}
	}
	// The legacy blob has now been fully represented by Markdown documents and
	// normalized rows. Clear it so there is only one content authority.
	if _, err := s.service.SaveAnnotations(ctx, pageID, ""); err != nil {
		return nil, err
	}
	return out, nil
}

func annotationNoteTitle(exact string) string {
	runes := []rune(strings.TrimSpace(exact))
	if len(runes) > 56 {
		runes = append(runes[:56], '…')
	}
	if len(runes) == 0 {
		return "Selection note"
	}
	return string(runes) + " — note"
}

// selectionNoteMarkdown updates the human-readable note while preserving any
// existing front matter and Source footer written by the browser extension.
func selectionNoteMarkdown(existing, exact, note string) string {
	frontMatter := ""
	sourceFooter := ""
	content := existing
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		start := strings.Index(content, "---")
		rest := content[start+3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			cut := start + 3 + end + 4
			frontMatter = strings.TrimSpace(content[:cut]) + "\n\n"
			content = content[cut:]
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Source:") {
			sourceFooter = strings.TrimSpace(line)
			break
		}
	}
	var quoted []string
	for _, line := range strings.Split(strings.TrimSpace(exact), "\n") {
		if line == "" {
			quoted = append(quoted, ">")
		} else {
			quoted = append(quoted, "> "+line)
		}
	}
	if note == "" {
		note = "_No note._"
	}
	var b strings.Builder
	b.WriteString(frontMatter)
	b.WriteString(strings.Join(quoted, "\n"))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(note))
	if sourceFooter != "" {
		b.WriteString("\n\n")
		b.WriteString(sourceFooter)
	}
	b.WriteByte('\n')
	return b.String()
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
	// Star italics only: `_x_` is skipped on purpose — unescaping turns
	// `\_` into `_` and stripping word-internal underscores would corrupt
	// identifiers like tool_execution_start. Both sides of a match use the
	// same transform, so consistency matters more than completeness.
	reEmph   = regexp.MustCompile(`\*([^*\n]+)\*`)
	reCode   = regexp.MustCompile("`([^`\n]*)`")
	reSpaces = regexp.MustCompile(`[ \t\x{00a0}]+`)
	// Markdown backslash escapes, as emitted by Turndown (\_ \* \[ ...).
	reUnescape = regexp.MustCompile(`\\([\\` + "`" + `*_{}\[\]()#+\-.!~|])`)
)

func unescapeMarkdown(s string) string {
	return reUnescape.ReplaceAllString(s, "$1")
}

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

// cleanInlineMarkdown strips inline Markdown markup from one line of text.
// Unescape first, so Turndown artifacts like \_ and **** disappear the same
// way on the page side and the excerpt side.
func cleanInlineMarkdown(s string) string {
	s = unescapeMarkdown(s)
	s = reImage.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = reBold.ReplaceAllString(s, "")
	s = reEmph.ReplaceAllString(s, "$1")
	s = reCode.ReplaceAllString(s, "$1")
	s = reSpaces.ReplaceAllString(s, " ")
	return s
}

// denseRunes removes ALL whitespace and records, for each output rune, the
// rune index it came from in the input. Selections, Markdown sources, and
// rendered text disagree about spacing around inline code / CJK punctuation,
// so matching ignores whitespace entirely.
func denseRunes(s string) (string, []int) {
	runes := []rune(s)
	var b strings.Builder
	mapping := make([]int, 0, len(runes))
	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == '\u00a0' {
			continue
		}
		b.WriteRune(r)
		mapping = append(mapping, i)
	}
	return b.String(), mapping
}

// findExcerptOffsets locates the excerpt in canonical text with flexible
// whitespace, returning rune offsets into the un-collapsed canonical text.
func findExcerptOffsets(canonical, excerpt string) (int, int, bool) {
	denseC, mapping := denseRunes(canonical)
	denseE, _ := denseRunes(excerpt)
	if denseE == "" {
		return 0, 0, false
	}
	idx := strings.Index(denseC, denseE)
	if idx < 0 {
		return 0, 0, false
	}
	startRune := len([]rune(denseC[:idx]))
	endRune := startRune + len([]rune(denseE)) - 1
	if startRune >= len(mapping) || endRune >= len(mapping) {
		return 0, 0, false
	}
	start := mapping[startRune]
	end := mapping[endRune] + 1
	if end > len([]rune(canonical)) {
		end = len([]rune(canonical))
	}
	return start, end, true
}

// jsStringLength counts UTF-16 code units the way JavaScript's String.length
// does; Miru verifies sourceLength against markdown.length.
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
