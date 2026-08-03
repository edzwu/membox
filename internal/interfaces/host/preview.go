package host

import "strings"

// DocumentPreview extracts a short human-readable preview from a Markdown
// document: front matter, images, headings and quote markers are dropped, and
// the first meaningful paragraphs are joined into one line.
func DocumentPreview(body []byte, maxRunes int) string {
	text := string(body)
	// Strip YAML front matter.
	trimmed := strings.TrimLeft(text, "\ufeff \t\r\n")
	if strings.HasPrefix(trimmed, "---") {
		rest := trimmed[3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			rest = rest[end+4:]
		}
		text = rest
	}
	var lines []string
	started := false // crossed the excerpt/quote region of selection notes
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			if started && len(lines) > 0 {
				// Stop at the first paragraph gap so previews stay tight.
				break
			}
			continue
		}
		if strings.HasPrefix(s, "![") || strings.HasPrefix(s, "<img") {
			continue
		}
		// Headings are structural, not content — keep reading for real prose.
		if strings.HasPrefix(s, "#") {
			started = true
			continue
		}
		// Blockquotes are the clipped *excerpt* in selection notes (and
		// citations in pages) — the preview wants the note/prose, not the quote.
		if strings.HasPrefix(s, ">") {
			started = true
			continue
		}
		// Trailing attribution added by the clipper.
		if strings.HasPrefix(s, "Source:") {
			break
		}
		s = strings.TrimLeft(s, " \t")
		s = strings.TrimLeft(s, "-*")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Drop inline image syntax leftovers.
		s = strings.TrimSpace(strings.ReplaceAll(s, "](", "] ("))
		started = true
		lines = append(lines, stripInlineMarkdown(s))
	}
	out := strings.Join(lines, " ")
	if maxRunes <= 0 {
		return out
	}
	runes := []rune(out)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return out
}

// stripInlineMarkdown removes emphasis/code markers and bracket noise so the
// preview reads as plain prose.
func stripInlineMarkdown(s string) string {
	var b strings.Builder
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '*', '_', '`', '~':
			continue
		case '[', ']':
			c = ' '
		}
		if c == ' ' && prev == ' ' {
			continue
		}
		b.WriteByte(c)
		prev = c
	}
	return strings.TrimSpace(b.String())
}
