package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"membox"
	"membox/internal/interfaces/host"
)

func (m Model) View() string {
	if m.fullscreen {
		view := m.fullscreenView()
		if m.helpVisible {
			view = overlayModal(view, m.helpModal(), m.width)
		}
		return view
	}
	contentHeight := m.visibleRows()
	var content string
	if m.viewMode == viewBoard {
		content = m.boardView()
	} else {
		content = m.treePreviewView()
	}
	lines := strings.Split(content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	parts := []string{strings.Join(lines, "\n")}
	if m.detailsVisible {
		parts = append(parts, m.detailsView())
	}
	if m.deleteConfirm {
		parts = append(parts, m.deleteConfirmView())
	}
	if m.webQuitPrompt {
		parts = append(parts, m.webQuitPromptView())
	}
	if m.configVisible {
		parts = append(parts, m.configPanelView())
	}
	if m.inputVisible {
		parts = append(parts, m.inputView())
	}
	parts = append(parts, m.statusBar())
	view := strings.Join(parts, "\n")
	if m.helpVisible {
		view = overlayModal(view, m.helpModal(), m.width)
	}
	return view
}

func (m Model) detailsView() string {
	document, ok := m.selectedDocument()
	border := lipgloss.NewStyle().Width(max(10, m.width-2)).MaxWidth(max(10, m.width-2)).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderMuted)
	if !ok {
		return border.Render(dimStyle.Render("no document selected"))
	}
	id := fitWidth(document.ID, 36)
	path := fitWidth(document.Path, max(20, m.width-20))
	left := dimStyle.Render("id      ") + id
	right := dimStyle.Render("path    ") + path
	timeLine := dimStyle.Render("modified ") + dateOnly(document.UpdatedAt)
	first := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", max(2, m.width-4-lipgloss.Width(left)-lipgloss.Width(right))), right)
	content := fitWidth(first, max(10, m.width-4)) + "\n" + fitWidth(timeLine, max(10, m.width-4))
	return border.Render(content)
}

func (m Model) deleteConfirmView() string {
	width := max(10, m.width-2)
	border := lipgloss.NewStyle().Width(width).MaxWidth(width).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.Error)
	name := filepath.Base(m.deletePath)
	question := "Move " + name + " to trash?"
	hint := "y confirm • n/esc cancel • restore later with mm trash restore"
	content := fitWidth(errorStyle.Render(question), width) + "\n" + fitWidth(dimStyle.Render(hint), width)
	return border.Render(content)
}

// filterOptionsView is the match-semantics panel opened with ctrl+o while the
// filter input is focused: 全匹配 (contains ⇄ exact) and 大小写 (case).
// Both are orthogonal to the name/content scope chip; the status bar segment
// reflects them immediately.
func (m Model) filterOptionsView() string {
	width := max(10, m.width-2)
	border := lipgloss.NewStyle().Width(width).MaxWidth(width).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderAccent)
	rows := [][2]string{
		{"match", "contains"}, // label, off-option (on = whole word)
		{"case", "ignore"},    // label, off-option (on = sensitive)
	}
	lines := []string{accentStyle.Render("filter options")}
	for index, row := range rows {
		label := fitWidth(row[0], 10)
		options := []string{}
		onValue, offValue := "exact", row[1]
		if index == 1 {
			onValue = "sensitive"
		}
		on := (index == 0 && m.filterExact) || (index == 1 && m.filterCase)
		if on {
			options = append(options, dimStyle.Render(" "+offValue+" "), accentStyle.Render("["+onValue+"]"))
		} else {
			options = append(options, accentStyle.Render("["+offValue+"]"), dimStyle.Render(" "+onValue+" "))
		}
		rowText := label + " " + strings.Join(options, "")
		if index == m.filterOptionsSelected {
			rowText = lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Width(width).Inline(true).Render("> " + rowText)
		} else {
			rowText = lipgloss.NewStyle().Width(width).Inline(true).Render("  " + rowText)
		}
		lines = append(lines, rowText)
	}
	lines = append(lines, dimStyle.Render("↑↓ select • ←→ toggle • esc close"))
	return border.Render(strings.Join(lines, "\n"))
}

// updateConfigPanel handles keys while the settings panel is open: ↑↓ selects
// a row, ←→ cycles the value (saving immediately), esc/enter closes.
func (m Model) configPanelView() string {
	width := max(10, m.width-2)
	border := lipgloss.NewStyle().Width(width).MaxWidth(width).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderAccent)
	lines := m.webPanelLines(width)
	lines = append(lines, accentStyle.Render("settings"))
	for index, setting := range m.settings {
		label := fitWidth(setting.Label, 10)
		var options []string
		for _, option := range setting.Options {
			shown := option
			if setting.Key == "main_path" {
				shown = shortPathLabel(option)
			}
			if option == setting.Value {
				options = append(options, accentStyle.Render("["+shown+"]"))
			} else {
				options = append(options, dimStyle.Render(" "+shown+" "))
			}
		}
		if len(options) == 0 && setting.Key == "main_path" {
			options = append(options, dimStyle.Render("(no paths — mm path add)"))
		}
		row := label + " " + strings.Join(options, "")
		if index == m.configSelected {
			row = lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Width(width).Inline(true).Render("> " + row)
		} else {
			row = lipgloss.NewStyle().Width(width).Inline(true).Render("  " + row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, dimStyle.Render("↑↓ select • ←→ change • o open web • x stop/start • esc close"))
	return border.Render(strings.Join(lines, "\n"))
}

func shortPathLabel(path string) string {
	path = filepath.Clean(path)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home || strings.HasPrefix(path, home+string(filepath.Separator)) {
			path = "~" + strings.TrimPrefix(path, home)
		}
	}
	if len(path) <= 28 {
		return path
	}
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return path[len(path)-28:]
}

func (m Model) fullscreenView() string {
	header := accentStyle.Render("membox")
	if m.fullDocument != nil {
		header += dimStyle.Render("  " + shortID(m.fullDocument.ID) + "  " + displayTitle(m.fullDocument.Title, m.fullDocument.Path))
	}
	footer := dimStyle.Render(fmt.Sprintf("%3.0f%%  ↑/↓ line • pgup/pgdn page • q close", m.preview.ScrollPercent()*100))
	return header + "\n" + m.preview.View() + "\n" + footer
}

func (m Model) treeVisibleIndices() []int {
	visible := m.visibleRows()
	pinned := m.pinnedCount()
	if pinned == 0 {
		start := min(max(0, m.scrollTop), max(0, len(m.filtered)-visible))
		end := min(len(m.filtered), start+visible)
		indices := make([]int, 0, end-start)
		for index := start; index < end; index++ {
			indices = append(indices, index)
		}
		return indices
	}
	if pinned >= visible {
		start := 0
		if m.selected < pinned && m.selected >= visible {
			start = m.selected - visible + 1
		}
		end := min(pinned, start+visible)
		indices := make([]int, 0, end-start)
		for index := start; index < end; index++ {
			indices = append(indices, index)
		}
		return indices
	}
	indices := make([]int, 0, visible)
	for index := 0; index < pinned; index++ {
		indices = append(indices, index)
	}
	unpinnedVisible := visible - pinned
	start := min(max(pinned, m.scrollTop), max(pinned, len(m.filtered)-unpinnedVisible))
	end := min(len(m.filtered), start+unpinnedVisible)
	for index := start; index < end; index++ {
		indices = append(indices, index)
	}
	return indices
}

func (m Model) treePreviewView() string {
	listWidth, previewWidth := m.layoutWidths()
	indices := m.treeVisibleIndices()
	uuidWidth := 4
	// readStatusMark (○/◐/● + space) takes two columns; keep it from
	// squeezing the filename.
	badgeWidth := 2
	filenameWidth := max(12, listWidth-uuidWidth-badgeWidth-8)
	if listWidth >= 72 {
		filenameWidth = max(16, listWidth-uuidWidth-badgeWidth-30)
	} else if listWidth >= 52 {
		filenameWidth = max(14, listWidth-uuidWidth-badgeWidth-20)
	}
	var lines []string
	for _, i := range indices {
		candidate := m.filtered[i]
		pin := ""
		if candidate.document.Pinned {
			pin = lipgloss.NewStyle().Foreground(colors.Warning).Bold(true).Render("▌") + " "
		}
		uuid := dimStyle.Render(fitWidth(shortID(candidate.document.ID), uuidWidth))
		filename := fitMiddle(candidate.filename, filenameWidth)
		dates := ""
		if listWidth >= 72 {
			created, updated := dateOnly(candidate.document.CreatedAt), dateOnly(candidate.document.UpdatedAt)
			dates = dimStyle.Render("  " + created + "  " + updated)
		} else if listWidth >= 52 {
			dates = dimStyle.Render("  " + dateOnly(candidate.document.UpdatedAt))
		}
		line := lipgloss.NewStyle().Width(listWidth - 2).MaxWidth(listWidth - 2).Inline(true).Render(pin + readStatusMark(candidate.document.ReadStatus) + uuid + "  " + filename + dates)
		if i == m.selected {
			line = lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Width(listWidth - 2).Inline(true).Render("> " + line)
		} else {
			line = lipgloss.NewStyle().Width(listWidth - 2).Inline(true).Render("  " + line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("No documents.")}
	}
	list := lipgloss.NewStyle().Width(listWidth).MaxWidth(listWidth).Render(strings.Join(lines, "\n"))
	preview := lipgloss.NewStyle().Width(previewWidth).MaxWidth(previewWidth).Render(m.preview.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", preview)
}

// boardLayout holds the rendered board rows plus each card's vertical span
// (top row and height in canvas rows), keyed by document ID. It is used both to
// render the board and to scroll the selected card into view.
type boardLayout struct {
	rows      []string
	cardSpans map[string][2]int // document ID -> {topRow, height}
}

// computeBoard filters to documents with summaries and lays them out as a
// masonry grid using shortest-column placement (matching cli_dev's
// computeMasonryLayout). Card heights are dynamic (full summary content).
func (m Model) boardView() string {
	if m.graphFocusID != "" {
		return m.graphBoardView()
	}
	layout := m.computeBoard()
	if len(layout.rows) == 0 {
		return dimStyle.Render("No documents with summaries. ( summaries will be added by LLM in the future )")
	}
	rows := layout.rows
	if m.boardScrollY > 0 {
		if m.boardScrollY < len(rows) {
			rows = rows[m.boardScrollY:]
		} else {
			rows = nil
		}
	}
	return strings.Join(rows, "\n")
}

func (m Model) graphBoardView() string {
	var rows []string
	if m.graphLayout == "star" {
		rows, _ = m.graphStarRows()
	}
	if len(rows) == 0 {
		rows, _ = m.graphRows()
	}
	if len(rows) == 0 {
		return dimStyle.Render("No linked documents.")
	}
	if m.boardScrollY > 0 {
		if m.boardScrollY < len(rows) {
			rows = rows[m.boardScrollY:]
		} else {
			rows = nil
		}
	}
	return strings.Join(rows, "\n")
}

// visibleGraphIndices returns the indexes into m.graphCards that survive the
// current filters, mirroring the document-list semantics: name-mode filters
// match title/filename/path live, full-mode filters match body content via
// FTS (hits arrive async), and date filters always apply. The focus card
// (index 0) stays visible so the thread keeps its anchor.
func (m Model) visibleGraphIndices() []int {
	if len(m.graphCards) == 0 {
		return nil
	}
	indices := []int{0}
	nameFilters := m.effectiveNameFilters()
	fullQuery := m.fullTextFilterQuery()
	// Only apply body hits once they belong to the current query; before the
	// async result arrives the thread keeps showing its cards.
	fullReady := fullQuery != "" && m.graphSearchQuery == fullQuery && m.graphSearchHits != nil
	if len(nameFilters) == 0 && len(m.dateFilters) == 0 && !fullReady {
		for i := 1; i < len(m.graphCards); i++ {
			indices = append(indices, i)
		}
		return indices
	}
	for original, doc := range m.graphCards[1:] {
		if fullReady && !m.graphSearchHits[doc.ID] {
			continue
		}
		candidate := documentItems([]membox.DocumentView{doc})[0]
		if matchesTextFilters(candidate.title, candidate.filename, candidate.match, nameFilters) && matchesDateFilters(doc, m.dateFilters) {
			indices = append(indices, original+1)
		}
	}
	return indices
}

// graphRows flattens the thread tree into display lines and records, for each
// card in m.graphCards, the line index where that card's block starts (used
// for selection highlighting and scroll-into-view).
func (m Model) graphRows() ([]string, []int) {
	if len(m.graphCards) == 0 {
		return nil, nil
	}
	width := max(24, m.width-8)
	var lines []string
	starts := make([]int, 0, len(m.graphCards))

	selectedLine := func(line string) string {
		return lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Width(width).Inline(true).Render(line)
	}

	// Focus card (index 0).
	center := m.graphCards[0]
	centerItem := documentItems([]membox.DocumentView{center})[0]
	starts = append(starts, len(lines))
	header := "● focus  [" + shortID(center.ID) + "] " + centerItem.filename
	if m.graphSelected == 0 {
		lines = append(lines, selectedLine("> "+header))
	} else {
		lines = append(lines, accentStyle.Render("● focus")+dimStyle.Render("  ["+shortID(center.ID)+"] "+centerItem.filename))
	}
	if title := displayTitle(center.Title, center.Path); !titleRedundant(title, centerItem.filename) {
		for _, line := range wrapText(title, width-2) {
			lines = append(lines, dimStyle.Render("  ")+line)
		}
	}
	if center.Summary != "" {
		for _, line := range wrapText(center.Summary, width-2) {
			lines = append(lines, dimStyle.Render("  ")+mutedStyle.Render(line))
		}
	}
	if preview := m.graphPreviews[center.ID]; preview != "" {
		for _, line := range wrapText(preview, width-6) {
			lines = append(lines, dimStyle.Render("  ")+previewStyle.Render(line))
		}
	}

	visible := m.visibleGraphIndices()
	if len(visible) > 1 {
		related := visible[1:]
		for position, original := range related {
			candidate := documentItems([]membox.DocumentView{m.graphCards[original]})[0]
			lines = append(lines, dimStyle.Render("│"))
			starts = append(starts, len(lines))
			direction := "→"
			if original >= len(m.graphCards)-m.graphIncoming {
				direction = "←"
			}
			branch := "├─"
			continuation := "│ "
			if position+1 == len(related) {
				branch = "└─"
				continuation = "  "
			}
			header := branch + " " + direction + " [" + shortID(candidate.document.ID) + "] " + candidate.filename
			if position+1 == m.graphSelected {
				lines = append(lines, selectedLine("> "+header))
			} else {
				lines = append(lines, dimStyle.Render(branch)+" "+accentStyle.Render(direction)+" ["+shortID(candidate.document.ID)+"] "+candidate.filename)
			}
			if title := displayTitle(candidate.document.Title, candidate.document.Path); !titleRedundant(title, candidate.filename) {
				for _, line := range wrapText(title, width-2) {
					lines = append(lines, dimStyle.Render(continuation)+" "+line)
				}
			}
			if candidate.document.Summary != "" {
				for _, line := range wrapText(candidate.document.Summary, width-2) {
					lines = append(lines, dimStyle.Render(continuation)+" "+mutedStyle.Render(line))
				}
			}
			if preview := m.graphPreviews[candidate.document.ID]; preview != "" {
				previewLines := wrapText(preview, width-6)
				if len(previewLines) > 3 {
					previewLines = previewLines[:3]
				}
				for _, line := range previewLines {
					lines = append(lines, dimStyle.Render(continuation)+" "+previewStyle.Render(line))
				}
			}
		}
	}
	return lines, starts
}

// visibleStarSegments returns the graphCards indexes of the filtered incoming
// and outgoing neighbors (focus is always visible and sits between them).
func (m Model) visibleStarSegments() ([]int, []int) {
	visible := map[string]bool{}
	for _, idx := range m.visibleGraphIndices() {
		visible[m.graphCards[idx].ID] = true
	}
	outCount := len(m.graphCards) - 1 - m.graphIncoming
	var incoming, outgoing []int
	for i := 0; i < m.graphIncoming; i++ {
		idx := 1 + outCount + i
		if visible[m.graphCards[idx].ID] {
			incoming = append(incoming, idx)
		}
	}
	for i := 0; i < outCount; i++ {
		idx := 1 + i
		if visible[m.graphCards[idx].ID] {
			outgoing = append(outgoing, idx)
		}
	}
	return incoming, outgoing
}

// graphStarRows lays out the one-hop neighborhood as a star canvas: backlinks
// in the left column, the focus card (plus topics) in the center, links in
// the right column, with an arrow connector at the focus row. Starts align
// with the star selection order: incoming…, focus, outgoing…. Returns nil
// when the terminal is too narrow for three columns (callers fall back to the
// thread tree).
func (m Model) graphStarRows() ([]string, []int) {
	if len(m.graphCards) == 0 {
		return nil, nil
	}
	const connectorW = 3
	avail := m.width - 2*connectorW - 4
	if avail/3 < 20 {
		return nil, nil
	}
	colW := min(38, avail/3)

	visIn, visOut := m.visibleStarSegments()
	focusStarIndex := len(visIn)

	renderColumn := func(cardIdxs []int, starBase int) ([]string, []int) {
		var rows []string
		var starts []int
		for i, idx := range cardIdxs {
			starts = append(starts, len(rows))
			candidate := documentItems([]membox.DocumentView{m.graphCards[idx]})[0]
			rows = append(rows, m.renderCard(candidate, colW, starBase+i == m.graphSelected)...)
			rows = append(rows, "")
		}
		return rows, starts
	}

	leftRows, inStarts := renderColumn(visIn, 0)
	rightRows, outStarts := renderColumn(visOut, focusStarIndex+1)

	focusItem := documentItems([]membox.DocumentView{m.graphCards[0]})[0]
	focusRows := m.renderCard(focusItem, colW, m.graphSelected == focusStarIndex)
	focusCardHeight := len(focusRows)
	if len(m.graphTopics) > 0 {
		names := make([]string, 0, len(m.graphTopics))
		for _, topic := range m.graphTopics {
			names = append(names, "#"+topic.Name)
		}
		for _, line := range wrapText(strings.Join(names, " "), colW-2) {
			focusRows = append(focusRows, dimStyle.Render(line))
		}
	}

	height := max(len(leftRows), len(focusRows), len(rightRows))
	pad := func(rows []string) []string {
		for len(rows) < height {
			rows = append(rows, strings.Repeat(" ", colW))
		}
		return rows
	}
	leftRows, focusRows, rightRows = pad(leftRows), pad(focusRows), pad(rightRows)

	// Arrow connectors point into the focus card (backlinks) and out of it
	// (links), drawn at the vertical center of the focus card.
	blankConn := strings.Repeat(" ", connectorW)
	leftConn := make([]string, height)
	rightConn := make([]string, height)
	for i := 0; i < height; i++ {
		leftConn[i], rightConn[i] = blankConn, blankConn
	}
	// Place the arrows at the vertical center of the focus card body, not the
	// topic lines appended below it.
	mid := min(height-1, max(0, focusCardHeight/2))
	leftConn[mid] = "──→"
	rightConn[mid] = "─→ "

	rows := make([]string, height)
	for i := 0; i < height; i++ {
		rows[i] = leftRows[i] + leftConn[i] + focusRows[i] + rightConn[i] + rightRows[i]
	}

	starts := make([]int, 0, len(inStarts)+1+len(outStarts))
	starts = append(starts, inStarts...)
	starts = append(starts, 0)
	starts = append(starts, outStarts...)
	return rows, starts
}

// openGraphSelection opens the focused thread-tree card with the configured
// viewer. The tree keeps its original root — opening a linked document never
// re-roots the graph.
func (m Model) renderCard(candidate item, width int, selected bool) []string {
	if width < 8 {
		width = 8
	}
	innerW := width - 4 // 2 for border + 2 for padding

	var lines []string

	// Top border with selection indicator
	if selected {
		lines = append(lines, accentStyle.Render("╭"+strings.Repeat("─", innerW+2)+"╮"))
	} else {
		lines = append(lines, dimStyle.Render("╭"+strings.Repeat("─", innerW+2)+"╮"))
	}

	// Title line: [uuid] filename
	uuidPrefix := "[" + shortID(candidate.document.ID) + "]"
	title := fitWidth(uuidPrefix+" "+candidate.filename, innerW)
	if selected {
		lines = append(lines, accentStyle.Render("│ ")+accentStyle.Bold(true).Render(padWidth(title, innerW))+accentStyle.Render(" │"))
	} else {
		lines = append(lines, dimStyle.Render("│ ")+padWidth(title, innerW)+dimStyle.Render(" │"))
	}

	// Separator
	if selected {
		lines = append(lines, accentStyle.Render("├"+strings.Repeat("─", innerW+2)+"┤"))
	} else {
		lines = append(lines, dimStyle.Render("├"+strings.Repeat("─", innerW+2)+"┤"))
	}

	// Summary body (full content, card height grows with content)
	summary := candidate.document.Summary
	if summary == "" {
		summary = "(no summary)"
	}
	summaryLines := wrapText(summary, innerW)
	for _, sl := range summaryLines {
		lines = append(lines, dimStyle.Render("│ ")+padWidth(sl, innerW)+dimStyle.Render(" │"))
	}

	// Bottom border
	if selected {
		lines = append(lines, accentStyle.Render("╰"+strings.Repeat("─", innerW+2)+"╯"))
	} else {
		lines = append(lines, dimStyle.Render("╰"+strings.Repeat("─", innerW+2)+"╯"))
	}

	return lines
}

// wrapText wraps text to the given display width. It is grapheme-aware and
// breaks long words (e.g. CJK runs with no spaces) at character boundaries when
// necessary, so a line never exceeds width and nothing is truncated.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	wrapped := ansi.Wrap(text, width, " ")
	return strings.Split(wrapped, "\n")
}

func (m Model) commandMenuRows() int {
	if !m.cmdMenuVisible {
		return 0
	}
	return min(8, max(1, len(m.cmdSuggestions)))
}

func (m Model) tagsView() string {
	if m.filterCount() == 0 {
		return ""
	}
	modifiedStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Dark: "#3159b8", Light: "#d9e8ff"}).Foreground(lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#1b3a5b"}).Bold(true)
	createdStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Dark: "#8a5a00", Light: "#ffe2a8"}).Foreground(lipgloss.AdaptiveColor{Dark: "#fff7e6", Light: "#5b3900"}).Bold(true)
	textStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Dark: "#7048a8", Light: "#eadcff"}).Foreground(lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#3c1f63"}).Bold(true)
	type renderedTag struct {
		sequence uint64
		value    string
	}
	tags := make([]renderedTag, 0, m.filterCount())
	for _, filter := range m.dateFilters {
		style := modifiedStyle
		if filter.Field == dateFilterCreated {
			style = createdStyle
		}
		tags = append(tags, renderedTag{sequence: filter.Sequence, value: style.Render(" " + filter.Label + " ")})
	}
	for _, filter := range m.textFilters {
		// Scope: N = name, C = content (full-text). Match semantics ride on
		// the name prefix: N= exact, Nc case-sensitive, N=c both.
		switch filter.Mode {
		case searchModeFull:
			tags = append(tags, renderedTag{sequence: filter.Sequence, value: textStyle.Render(" C: " + filter.Value + " ")})
		default:
			prefix := "N"
			if filter.Exact {
				prefix += "="
			}
			if filter.Case {
				prefix += "c"
			}
			tags = append(tags, renderedTag{sequence: filter.Sequence, value: textStyle.Render(" " + prefix + ": " + filter.Value + " ")})
		}
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].sequence < tags[j].sequence })
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		parts = append(parts, tag.value)
	}
	return ansi.Truncate(strings.Join(parts, " "), max(10, m.width-2), "…")
}

func (m Model) inputView() string {
	value := m.input.View()
	innerWidth := max(10, m.width-2)
	badge := m.modeBadge()
	fieldWidth := max(6, innerWidth-lipgloss.Width(badge)-1)
	field := lipgloss.NewStyle().Width(fieldWidth).MaxWidth(fieldWidth).Inline(true).Render(value)
	line := lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Inline(true).Render(badge + " " + field)
	border := lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderAccent)
	input := border.Render(line)
	if m.filterOptionsVisible {
		return m.filterOptionsView() + "\n" + input
	}
	if menu := m.commandMenuView(); menu != "" {
		return menu + "\n" + input
	}
	if tags := m.tagsView(); tags != "" {
		return tags + "\n" + input
	}
	return input
}

func (m Model) commandMenuView() string {
	if !m.cmdMenuVisible {
		return ""
	}
	innerWidth := max(10, m.width-2)
	if len(m.cmdSuggestions) == 0 {
		return dimStyle.Render("no suggestions")
	}
	rows := m.commandMenuRows()
	start := min(max(0, m.cmdSelected-rows+1), max(0, len(m.cmdSuggestions)-rows))
	end := min(len(m.cmdSuggestions), start+rows)
	var lines []string
	for index := start; index < end; index++ {
		suggestion := m.cmdSuggestions[index]
		value := fitWidth(suggestion.Display, 14)
		description := fitWidth(suggestion.Description, max(10, innerWidth-16))
		line := value + "  " + dimStyle.Render(description)
		if index == m.cmdSelected {
			line = lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Width(innerWidth).Inline(true).Render("> " + line)
		} else {
			line = lipgloss.NewStyle().Width(innerWidth).Inline(true).Render("  " + line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) statusBar() string {
	width := max(20, m.width)
	left := dimStyle.Render(m.hints()) + "  " + m.webBadge() + m.agentStatusBarBadge()
	right := ""
	if m.loading {
		right = m.spinner.View() + " " + right
	}
	if m.deleteConfirm {
		right += errorStyle.Render("delete " + filepath.Base(m.deletePath))
	} else if m.filterErr != nil {
		right += errorStyle.Render("Invalid filter: " + m.filterErr.Error())
	} else if m.err != nil {
		right += errorStyle.Render("Error: " + m.err.Error())
	} else if m.statusMessage != "" {
		right += accentStyle.Render(m.statusMessage)
	} else {
		if m.inputVisible {
			mode := "name"
			switch m.inputMode {
			case inputModeCmd:
				mode = "cmd"
			case inputModeAgent:
				mode = "agent"
			default:
				if m.searchMode == searchModeFull {
					mode = "content"
				}
			}
			right += accentStyle.Render(mode)
			// Match semantics (≈/=/Aa) are orthogonal to the scope chip; they
			// get their own status-bar segment so toggling is always visible.
			if m.inputMode == inputModeSearch && m.searchMode != searchModeFull {
				if m.filterExact {
					right += accentStyle.Render(" =")
				} else {
					right += dimStyle.Render(" ≈")
				}
				if m.filterCase {
					right += accentStyle.Render(" Aa")
				}
			}
			right += dimStyle.Render(fmt.Sprintf(" %d/%d", len(m.filtered), len(m.items)))
			if m.cmdMenuVisible {
				right += dimStyle.Render(fmt.Sprintf(" • %d suggestion(s)", len(m.cmdSuggestions)))
			}
		} else {
			if m.filterCount() > 0 {
				right += accentStyle.Render(fmt.Sprintf("%d tag(s) ", m.filterCount()))
			}
			if document, ok := m.selectedDocument(); ok {
				right += dimStyle.Render(shortID(document.ID) + " " + displayTitle(document.Title, document.Path))
			} else {
				right += dimStyle.Render(fmt.Sprintf("%d documents", len(m.items)))
			}
		}
	}
	maxRight := max(0, width-lipgloss.Width(left)-1)
	if lipgloss.Width(right) > maxRight {
		right = fitWidth(right, maxRight)
	}
	padding := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", padding) + right
}

func (m Model) modeBadge() string {
	mode := " NAME "
	background := lipgloss.AdaptiveColor{Dark: "#3159b8", Light: "#d9e8ff"}
	foreground := lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#1b3a5b"}
	switch m.inputMode {
	case inputModeCmd:
		mode = " CMD "
		background = lipgloss.AdaptiveColor{Dark: "#555555", Light: "#dddddd"}
		foreground = lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#333333"}
	case inputModeAgent:
		mode = " AGENT "
		background = lipgloss.AdaptiveColor{Dark: "#7048a8", Light: "#eadcff"}
		foreground = lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#3c1f63"}
	default:
		if m.searchMode == searchModeFull {
			mode = " CONTENT "
			background = lipgloss.AdaptiveColor{Dark: "#2f7d4a", Light: "#c9f0d8"}
			foreground = lipgloss.AdaptiveColor{Dark: "#f4fff8", Light: "#173f26"}
		}
	}
	return lipgloss.NewStyle().Background(background).Foreground(foreground).Bold(true).Inline(true).Render(mode)
}

// hints keeps the status bar quiet: every binding lives in the centered help
// modal (press h), which groups shortcuts by the surface they belong to.
func (m Model) hints() string {
	return "? help"
}

func searchResultItems(items []item, results []membox.SearchResult, dateFilters []dateFilter, nameFilters []textFilter, hideNotes bool) []item {
	allowed := make(map[string]bool, len(results))
	for _, result := range results {
		allowed[result.DocumentID] = true
	}
	filtered := make([]item, 0, len(results))
	for _, candidate := range items {
		if hideNotes && isClippedNote(candidate.filename) {
			continue
		}
		if allowed[candidate.document.ID] && matchesDateFilters(candidate.document, dateFilters) && matchesTextFilters(candidate.title, candidate.filename, candidate.match, nameFilters) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

// Clipped selection notes (*-note.md) stay out of the tree/board when
// hide_notes is on; they remain reachable via the viewer and link threads.
func isClippedNote(filename string) bool {
	return strings.HasSuffix(filename, "-note.md")
}

func documentItems(documents []membox.DocumentView) []item {
	items := make([]item, 0, len(documents))
	for _, document := range documents {
		filename := documentFilename(document)
		match := document.Title + " " + document.Path + " " + filename + " " + document.Status + " " + document.ID
		items = append(items, item{document: document, title: displayTitle(document.Title, document.Path), filename: filename, match: match})
	}
	return items
}

func documentFilename(document membox.DocumentView) string {
	if document.RelativePath != "" {
		return filepath.Base(document.RelativePath)
	}
	return filepath.Base(document.Path)
}

func displayTitle(title, path string) string {
	if title != "" {
		return title
	}
	if path == "" {
		return "(untitled)"
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// titleRedundant reports whether the displayed title adds nothing over the
// filename already shown on the card (e.g. clip notes whose title IS the slug).
func titleRedundant(title, filename string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	f := strings.ToLower(strings.TrimSpace(filename))
	if ext := filepath.Ext(f); ext != "" {
		f = strings.TrimSuffix(f, ext)
	}
	if t == "" || f == "" {
		return false
	}
	return t == f
}

func previewPlaceholder(message string) string {
	return headingStyle.Render("membox") + "\n\n" + dimStyle.Render(message)
}
func fitWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

// readStatusMark renders the reading-state indicator: ○ unread, ◐ reading,
// ● finished. Unread documents stay visually quiet (dim) so the "next to
// read" queue pops; finished is muted so finished backlog recedes.
func readStatusMark(status string) string {
	switch status {
	case "reading":
		return lipgloss.NewStyle().Foreground(colors.Accent).Render("◐") + " "
	case "finished":
		return lipgloss.NewStyle().Foreground(colors.Muted).Render("●") + " "
	default: // unread / ""
		return dimStyle.Render("○") + " "
	}
}

// padWidth truncates value to width (with ellipsis) then right-pads with spaces
// so the result has exactly the given display width. Used to keep card borders aligned.
func padWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	truncated := ansi.Truncate(value, width, "…")
	if w := ansi.StringWidth(truncated); w < width {
		return truncated + strings.Repeat(" ", width-w)
	}
	return truncated
}
func fitMiddle(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	left := width / 2
	right := width - left - 1
	return ansi.Cut(value, 0, left) + "…" + ansi.TruncateLeft(value, right, "")
}

func dateOnly(value time.Time) string {
	if value.IsZero() {
		return "----------"
	}
	return value.Local().Format("2006-01-02")
}

func shortID(id string) string { return host.ShortDocumentID(id) }
