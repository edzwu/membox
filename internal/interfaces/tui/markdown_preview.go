package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Lightweight terminal Markdown preview.
//
// Glamour looks nicer but is too heavy for the split-pane TUI (each selection
// change blocked on a full AST+style pass). This path is intentionally dumb
// and fast: line-oriented styling + cell-aware wrap. Good enough to read notes
// while keeping arrow keys snappy.

var (
	previewH1    = lipgloss.NewStyle().Bold(true).Foreground(colors.Heading)
	previewH2    = lipgloss.NewStyle().Bold(true).Foreground(colors.Accent)
	previewH3    = lipgloss.NewStyle().Bold(true).Foreground(colors.Text)
	previewQuote = lipgloss.NewStyle().Foreground(colors.Muted).Italic(true)
	previewCode  = lipgloss.NewStyle().Foreground(colors.Warning)
	previewDim   = lipgloss.NewStyle().Foreground(colors.Muted)
	previewHR    = lipgloss.NewStyle().Foreground(colors.BorderMuted)

	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reBold       = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	reItalic     = regexp.MustCompile(`\*([^*]+)\*|_([^_]+)_`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reImage      = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
)

// renderMarkdownPreview turns Markdown source into ANSI text for the TUI
// viewport. Front matter is stripped. Rendering is O(lines) and avoids glamour.
func renderMarkdownPreview(source string, width int) string {
	if width < 20 {
		width = 20
	}
	body := stripPreviewFrontmatter(source)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if looksLikePlainPlaceholder(body) {
		return wrapPlainPreview(body, width)
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fenced code blocks: dim monospace-ish plain text, no further MD.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			lang := strings.TrimSpace(strings.TrimLeft(trimmed, "`~"))
			if inFence && lang != "" {
				out = append(out, wrapStyled(previewDim.Render("┌ "+lang), width)...)
			} else if !inFence {
				out = append(out, wrapStyled(previewDim.Render("└"), width)...)
			}
			continue
		}
		if inFence {
			out = append(out, wrapStyled(previewCode.Render("  "+line), width)...)
			continue
		}

		switch {
		case trimmed == "":
			out = append(out, "")
		case isHorizontalRule(trimmed):
			rule := strings.Repeat("─", max(8, width-2))
			out = append(out, previewHR.Render(rule))
		case strings.HasPrefix(trimmed, "### "):
			out = append(out, wrapStyled(previewH3.Render(styleInline(strings.TrimSpace(trimmed[4:]))), width)...)
		case strings.HasPrefix(trimmed, "## "):
			out = append(out, wrapStyled(previewH2.Render(styleInline(strings.TrimSpace(trimmed[3:]))), width)...)
		case strings.HasPrefix(trimmed, "# "):
			out = append(out, wrapStyled(previewH1.Render(styleInline(strings.TrimSpace(trimmed[2:]))), width)...)
		case strings.HasPrefix(trimmed, ">"):
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			text = strings.TrimSpace(strings.TrimPrefix(text, " "))
			out = append(out, wrapStyled(previewQuote.Render("│ "+styleInline(text)), width)...)
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			text := styleInline(strings.TrimSpace(trimmed[2:]))
			out = append(out, wrapStyled("  • "+text, width)...)
		case isOrderedList(trimmed):
			// Keep "1. " prefix, style the rest.
			idx := strings.IndexByte(trimmed, ' ')
			prefix := trimmed
			rest := ""
			if idx > 0 {
				prefix = trimmed[:idx]
				rest = styleInline(strings.TrimSpace(trimmed[idx+1:]))
			}
			out = append(out, wrapStyled("  "+prefix+" "+rest, width)...)
		case strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2:
			// Tables: collapse to a single dim line without alignment math.
			cells := splitTableRow(trimmed)
			out = append(out, wrapStyled(previewDim.Render("  "+strings.Join(cells, " · ")), width)...)
		default:
			out = append(out, wrapStyled(styleInline(line), width)...)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func stripPreviewFrontmatter(source string) string {
	text := strings.TrimLeft(source, "\ufeff \t\r\n")
	if !strings.HasPrefix(text, "---") {
		return source
	}
	rest := text[3:]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	} else {
		return source
	}
	if end := strings.Index(rest, "\n---\n"); end >= 0 {
		return strings.TrimLeft(rest[end+5:], "\n")
	}
	if end := strings.Index(rest, "\n---\r\n"); end >= 0 {
		return strings.TrimLeft(rest[end+6:], "\n")
	}
	if end := strings.Index(rest, "\n---"); end >= 0 && end+4 == len(rest) {
		return ""
	}
	return source
}

func looksLikePlainPlaceholder(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "PDF document") {
		return true
	}
	if strings.HasPrefix(trimmed, "No document") || strings.HasPrefix(trimmed, "Loading ") {
		return true
	}
	return false
}

func wrapPlainPreview(text string, width int) string {
	if width < 8 {
		width = 8
	}
	return strings.TrimRight(ansi.Wrap(text, width, ""), "\n")
}

func wrapStyled(line string, width int) []string {
	if width < 8 {
		width = 8
	}
	if ansi.StringWidth(line) <= width {
		return []string{line}
	}
	// ansi.Wrap is cell-aware; preserves most SGR when breaking.
	return strings.Split(ansi.Wrap(line, width, ""), "\n")
}

func styleInline(s string) string {
	s = reImage.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := reInlineCode.FindStringSubmatch(m)
		if len(inner) < 2 {
			return m
		}
		return previewCode.Render(inner[1])
	})
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		parts := reBold.FindStringSubmatch(m)
		for i := 1; i < len(parts); i++ {
			if parts[i] != "" {
				return lipgloss.NewStyle().Bold(true).Render(parts[i])
			}
		}
		return m
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		parts := reItalic.FindStringSubmatch(m)
		for i := 1; i < len(parts); i++ {
			if parts[i] != "" {
				return lipgloss.NewStyle().Italic(true).Render(parts[i])
			}
		}
		return m
	})
	return s
}

func isHorizontalRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	only := true
	for _, r := range s {
		if r != '-' && r != '*' && r != '_' && r != ' ' {
			only = false
			break
		}
	}
	return only && (strings.Count(s, "-") >= 3 || strings.Count(s, "*") >= 3 || strings.Count(s, "_") >= 3)
}

func isOrderedList(s string) bool {
	// 1. item / 12. item
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '.' {
		return false
	}
	return i+1 < len(s) && s[i+1] == ' '
}

func splitTableRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Skip markdown separator rows like --- | :---:
		if p == "" || isTableSepCell(p) {
			continue
		}
		out = append(out, styleInline(p))
	}
	return out
}

func isTableSepCell(s string) bool {
	for _, r := range s {
		if r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return strings.Contains(s, "-")
}
