package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The help modal uses blue for keys, headers and the border so the shortcut
// reference reads as one quiet, clearly separated surface.
var (
	helpBlue   = lipgloss.AdaptiveColor{Dark: "#5f87ff", Light: "#3159b8"}
	helpTitle  = lipgloss.NewStyle().Foreground(helpBlue).Bold(true)
	helpHeader = lipgloss.NewStyle().Foreground(helpBlue).Bold(true)
	helpKey    = lipgloss.NewStyle().Foreground(helpBlue)
	helpDesc   = lipgloss.NewStyle().Foreground(colors.Muted)
)

type helpEntry struct {
	key  string
	desc string
}

type helpSection struct {
	title   string
	entries []helpEntry
}

// helpSections lists every binding grouped by the surface it belongs to. Keep
// this in sync with the key handling in model.go.
func helpSections() []helpSection {
	return []helpSection{
		{title: "General", entries: []helpEntry{
			{"ctrl+d", "quit"},
			{"ctrl+r", "rescan paths"},
			{"ctrl+o", "settings panel"},
			{"h / esc", "close this help"},
		}},
		{title: "Browse (tree / board)", entries: []helpEntry{
			{"↑ ↓ / j k", "move selection"},
			{"pgup / pgdn", "page up / down"},
			{"home / end", "first / last"},
			{"tab", "tree ⇄ board"},
			{"enter", "open document"},
			{"e", "edit in $EDITOR"},
			{"o", "open with system app"},
			{"space / space×2", "details / filter input"},
			{"s", "sort: name ⇄ newest"},
			{"p", "pin / unpin"},
			{"d", "delete"},
			{"q", "back / close panels"},
		}},
		{title: "Filter input", entries: []helpEntry{
			{"space", "add date tag"},
			{"enter", "add text tag / open"},
			{"tab", "name ⇄ full-text search"},
			{"backspace", "remove last tag"},
			{"ctrl+u", "clear line"},
			{"ctrl+p", "cycle: name ⇄ cmd ⇄ agent"},
			{"esc", "hide input"},
		}},
		{title: "Command palette", entries: []helpEntry{
			{"↑ ↓", "history / menu selection"},
			{"tab", "show suggestions"},
			{"enter", "run command"},
			{"esc", "close menu / hide input"},
		}},
		{title: "Settings panel", entries: []helpEntry{
			{"↑ ↓", "select setting"},
			{"← →", "cycle value (saves)"},
			{"esc / enter", "close"},
		}},
		{title: "Link thread", entries: []helpEntry{
			{"← → ↑ ↓", "walk linked documents"},
			{"space×2", "filter cards: name/full text"},
			{"enter", "open focused document"},
			{"q", "back to tree"},
		}},
		{title: "Fullscreen viewer", entries: []helpEntry{
			{"j / k, ↑ ↓", "scroll"},
			{"space / pgdn", "page down"},
			{"g / shift+g", "top / bottom"},
			{"q / esc", "back"},
		}},
	}
}

// helpContent builds the inner (border-less) lines of the help modal.
func (m Model) helpContent(innerWidth int) []string {
	sections := helpSections()
	colWidth := 0
	for _, section := range sections {
		for _, entry := range section.entries {
			colWidth = max(colWidth, ansi.StringWidth(entry.key))
		}
	}
	colWidth += 3

	lines := []string{
		lipgloss.NewStyle().Width(innerWidth).Align(lipgloss.Center).Render(helpTitle.Render("Keyboard Help")),
		"",
	}
	for _, section := range sections {
		lines = append(lines, helpHeader.Render(section.title))
		for _, entry := range section.entries {
			line := "  " + padWidth(entry.key, colWidth) + helpDesc.Render(entry.desc)
			lines = append(lines, fitWidth(line, innerWidth))
		}
		lines = append(lines, "")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// helpModal renders the bordered help box, windowed when the terminal is too
// short, with a scroll indicator row.
func (m Model) helpModal() []string {
	outerWidth := min(max(52, m.width-8), 84)
	innerWidth := max(34, outerWidth-2)
	content := m.helpContent(innerWidth)

	maxHeight := max(8, m.height-6)
	innerHeight := len(content)
	scrollable := len(content) > maxHeight
	if scrollable {
		innerHeight = maxHeight - 1 // reserve the scroll indicator row
	}
	scroll := min(max(0, m.helpScroll), max(0, len(content)-innerHeight))
	window := content[scroll : scroll+innerHeight]
	if scrollable {
		window = append(window, dimStyle.Render(fmt.Sprintf("↑ ↓ scroll %d-%d/%d · h/esc close", scroll+1, scroll+innerHeight, len(content))))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(helpBlue).
		Width(innerWidth).
		Padding(0, 1).
		Render(strings.Join(window, "\n"))
	return strings.Split(box, "\n")
}

// overlayModal composites the modal as a separate top layer: only the cells
// the panel actually occupies are replaced, so the content underneath stays
// visible around it.
func overlayModal(base string, modal []string, width int) string {
	lines := strings.Split(base, "\n")
	modalWidth := 0
	for _, row := range modal {
		modalWidth = max(modalWidth, ansi.StringWidth(row))
	}
	left := max(0, (width-modalWidth)/2)
	top := max(0, (len(lines)-len(modal))/2)
	for index, row := range modal {
		line := top + index
		if line >= len(lines) {
			break
		}
		under := lines[line]
		before := ansi.Cut(under, 0, left)
		if pad := left - ansi.StringWidth(before); pad > 0 {
			before += strings.Repeat(" ", pad)
		}
		after := ansi.TruncateLeft(under, left+ansi.StringWidth(row), "")
		lines[line] = before + row + after
	}
	return strings.Join(lines, "\n")
}

// updateHelp scrolls the modal; any other key closes it.
func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.helpScroll = max(0, m.helpScroll-1)
	case "down", "j":
		m.helpScroll++
	case "pgup", "ctrl+b":
		m.helpScroll = max(0, m.helpScroll-10)
	case "pgdown", "ctrl+f", " ", "space":
		m.helpScroll += 10
	case "home", "g":
		m.helpScroll = 0
	case "end", "shift+g":
		m.helpScroll = 1 << 30
	default:
		m.helpVisible, m.helpScroll = false, 0
	}
	return m, nil
}
