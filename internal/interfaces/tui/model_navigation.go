package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"membox"
	"membox/internal/interfaces/host"
)

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	if m.deleteConfirm {
		switch msg.String() {
		case "y", "enter":
			m.loading = true
			selector := m.deleteSelector
			commands = append(commands, m.spinner.Tick, deleteDocumentCmd(m.ctx, m.app, selector))
		case "n", "esc", "q":
			m.deleteConfirm, m.deleteSelector, m.deletePath = false, "", ""
			m.statusMessage = "Delete canceled"
		}
		return m, tea.Batch(commands...)
	}
	// Walking the thread tree: arrows move between linked documents, enter
	// opens the focused one (and re-focuses the graph on it afterwards).
	if m.graphFocusID != "" {
		switch msg.String() {
		case "up", "down", "left", "right", "h", "k", "j", "l", "pgup", "pgdown", "home", "end":
			m.moveGraphSelection(msg.String())
			return m, nil
		case "enter":
			return m.openGraphSelection()
		}
	}
	// Tab gives the right preview pane keyboard focus. While focused, only
	// scrolling and lifecycle keys are handled here; the tree selection stays
	// fixed underneath it.
	if m.previewFocused {
		return m.updatePreviewNavigation(msg)
	}
	switch msg.String() {
	case "q", "esc":
		// Contextual back peels one surface at a time. Command-owned result
		// views get first chance to close; Ctrl+D remains the only quit key.
		if !m.closeCommandResultView() {
			if m.viewMode == viewBoard {
				m.viewMode = viewTree
				m.keepSelectionVisible()
			} else if m.detailsVisible {
				m.detailsVisible = false
			}
		}
		m.statusMessage = ""
		return m, nil
	case "enter":
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			if document.MediaType == "application/pdf" {
				commands = append(commands, m.spinner.Tick, openCmd(m.ctx, m.app, m.launcher, document.ID))
			} else if m.viewerMode == "web" {
				commands = append(commands, m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, document.ID))
			} else {
				commands = append(commands, m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
			}
		}
	case "space", " ":
		if m.lastKeyAt.Add(doubleSpaceWindow).After(time.Now()) {
			// Double space always opens the filter input, regardless of which
			// mode (cmd / agent) was used last.
			m.spaceSequence++
			m.lastKeyAt = time.Time{}
			commands = append(commands, m.openInput(inputModeSearch))
			return m, tea.Batch(commands...)
		}
		// first space: schedule delayed details toggle
		m.lastKeyAt = time.Now()
		m.spaceSequence++
		sequence := m.spaceSequence
		commands = append(commands, tea.Tick(doubleSpaceWindow, func(time.Time) tea.Msg { return spaceTimeoutMsg{sequence: sequence} }))
	case ":":
		// vim-style: ":" jumps straight to the command palette.
		m.spaceSequence, m.lastKeyAt = 0, time.Time{}
		return m, m.openInput(inputModeCmd)
	case "ctrl+k":
		// Direct agent input (terminals cannot deliver cmd+k).
		m.spaceSequence, m.lastKeyAt = 0, time.Time{}
		return m, m.openInput(inputModeAgent)
	case "tab":
		// Tab is contextual in the file tree: PDFs convert to linked Markdown;
		// ordinary documents keep the established tree → preview focus action.
		if document, ok := m.selectedDocument(); ok && document.MediaType == "application/pdf" {
			if m.pdfConversionActive {
				return m, nil
			}
			progressTick := m.beginPDFProgress(document.ID)
			commands = append(commands, m.spinner.Tick, startPDFConversionCmd(m.ctx, m.app, document.ID, m.pdfProgressSequence), progressTick)
			return m, tea.Batch(commands...)
		}
		if m.viewMode == viewTree {
			m.previewFocused = true
			m.statusMessage = ""
			m.resize()
		}
		return m, nil
	case "ctrl+t":
		// Toggle between tree and board view when input is not active
		m.graphFocusID = ""
		m.graphCards = nil
		m.graphSelected = 0
		m.graphIncoming = 0
		m.graphSearchQuery, m.graphSearchHits = "", nil
		m.graphLayout, m.graphTopics = "", nil
		m.previewFocused = false
		if m.viewMode == viewTree {
			m.viewMode = viewBoard
			// Snap the highlight to a card that is actually drawn on the board.
			m.snapToBoardSelection()
			m.scrollBoardToSelection()
		} else {
			m.viewMode = viewTree
			m.keepSelectionVisible()
		}
		return m, nil
	case "up", "down", "pgup", "pgdown", "k", "j":
		m.deleteConfirm, m.deleteSelector, m.deletePath = false, "", ""
		if msg.String() == "k" {
			return m.moveSelection("up")
		}
		if msg.String() == "j" {
			return m.moveSelection("down")
		}
		return m.moveSelection(msg.String())
	case "home", "end":
		if m.viewMode == viewTree {
			return m.moveSelection(msg.String())
		}
		return m, nil
	case "ctrl+p":
		return m, m.cycleMediaScope()
	case "s":
		// Summarize the selected document with the agent (same flow as
		// `mm doc summarize <id>`); the board keeps rendering while it runs.
		if document, ok := m.selectedDocument(); ok && !m.summarizing[document.ID] {
			m.summarizing[document.ID] = true
			m.statusMessage = "summarizing " + shortID(document.ID) + " …"
			commands = append(commands, summarizeCmd(m.ctx, m.app, document.ID))
		}
	case "p":
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			commands = append(commands, m.spinner.Tick, togglePinCmd(m.ctx, m.app, document.ID))
		}
	case "d":
		if document, ok := m.selectedDocument(); ok {
			m.deleteConfirm = true
			m.deleteSelector = document.ID
			m.deletePath = document.Path
			m.statusMessage = ""
		}
		return m, nil
	case "r":
		// Markdown rename changes the filesystem name. PDF rename is virtual:
		// it edits the catalog title while preserving the managed PDF path.
		if document, ok := m.selectedDocument(); ok {
			cmd := m.openInput(inputModeCmd)
			value := documentFilename(document)
			if document.MediaType == "application/pdf" {
				value = displayTitle(document.Title, document.Path)
			}
			m.input.SetValue("rename " + value)
			m.input.CursorEnd()
			return m, cmd
		}
	case "e":
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			commands = append(commands, m.spinner.Tick, resolveEditorCmd(m.ctx, m.app, document.ID))
		}
	case "o":
		if document, ok := m.selectedDocument(); ok {
			commands = append(commands, openCmd(m.ctx, m.app, m.launcher, document.ID))
		}
	case "ctrl+h":
		// Toggle selection-note (*-note.md) visibility; persisted as hide_notes.
		m.hideNotes = !m.hideNotes
		m.refreshFilter()
		m.keepSelectionVisible()
		value := "off"
		if m.hideNotes {
			value = "on"
		}
		m.statusMessage = "hide notes: " + value
		commands = append(commands, setSettingCmd(m.ctx, m.app, "hide_notes", value))
	default:
		m.spaceSequence = 0
		m.lastKeyAt = time.Time{}
		m.statusMessage = ""
	}
	return m, tea.Batch(commands...)
}

func (m Model) updatePreviewNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc", "q", "left", "h":
		m.previewFocused = false
		m.statusMessage = ""
		m.resize()
	case "up", "k":
		m.preview.LineUp(1)
	case "down", "j":
		m.preview.LineDown(1)
	case "pgup", "ctrl+b":
		m.preview.PageUp()
	case "pgdown", "ctrl+f", "space", " ":
		m.preview.PageDown()
	case "home", "g":
		m.preview.GotoTop()
	case "end", "shift+g":
		m.preview.GotoBottom()
	}
	return m, nil
}

// closeCommandResultView is the single lifecycle exit for full result
// surfaces opened by palette commands. Future command result views should add
// their cleanup here so both esc and q always return to the file tree.
func (m *Model) closeCommandResultView() bool {
	if m.graphFocusID == "" {
		return false
	}
	m.graphFocusID = ""
	m.graphCards = nil
	m.graphIncoming = 0
	m.graphSelected = 0
	m.graphSearchQuery, m.graphSearchHits = "", nil
	m.graphLayout, m.graphTopics = "", nil
	m.previewFocused = false
	m.viewMode = viewTree
	m.keepSelectionVisible()
	return true
}

func (m Model) moveSelection(key string) (tea.Model, tea.Cmd) {
	if m.viewMode == viewBoard {
		m.moveBoardSelection(key)
		m.scrollBoardToSelection()
	} else {
		switch key {
		case "up":
			if m.selected > 0 {
				m.selected--
			}
		case "down":
			if m.selected+1 < len(m.filtered) {
				m.selected++
			}
		case "pgup":
			m.selected = max(0, m.selected-m.visibleRows())
		case "pgdown":
			m.selected = min(max(0, len(m.filtered)-1), m.selected+m.visibleRows())
		case "home":
			m.selected = 0
		case "end":
			m.selected = max(0, len(m.filtered)-1)
		}
	}
	m.keepSelectionVisible()
	if document, ok := m.selectedDocument(); ok && m.rawDocumentID == document.ID {
		m.applyPreviewContent()
		return m, nil
	}
	return m, m.loadPreview()
}

// boardVisibleIndices returns the indices into m.filtered of the cards actually
// rendered on the board (documents with a non-empty summary), in display order.
func (m Model) boardVisibleIndices() []int {
	var indices []int
	for i, candidate := range m.filtered {
		if candidate.document.Summary != "" {
			indices = append(indices, i)
		}
	}
	return indices
}

// moveBoardSelection moves the selection only among board-visible cards, so the
// highlight never lands on a card that is not drawn.
func (m *Model) moveBoardSelection(key string) {
	indices := m.boardVisibleIndices()
	if len(indices) == 0 {
		return
	}
	// Locate the current selection within the visible indices; if it is not a
	// visible card, snap to the nearest visible one.
	pos := -1
	for p, idx := range indices {
		if idx == m.selected {
			pos = p
			break
		}
	}
	if pos == -1 {
		m.snapToBoardSelection()
		for p, idx := range indices {
			if idx == m.selected {
				pos = p
				break
			}
		}
		if pos == -1 {
			return
		}
	}
	switch key {
	case "up":
		if pos > 0 {
			m.selected = indices[pos-1]
		}
	case "down":
		if pos+1 < len(indices) {
			m.selected = indices[pos+1]
		}
	case "pgup":
		m.selected = indices[max(0, pos-m.visibleRows())]
	case "pgdown":
		m.selected = indices[min(len(indices)-1, pos+m.visibleRows())]
	}
}

// snapToBoardSelection ensures the selection points at a board-visible card,
// choosing the nearest visible index at or after the current selection.
func (m *Model) snapToBoardSelection() {
	indices := m.boardVisibleIndices()
	if len(indices) == 0 {
		return
	}
	for _, idx := range indices {
		if idx == m.selected {
			return
		}
	}
	for _, idx := range indices {
		if idx >= m.selected {
			m.selected = idx
			return
		}
	}
	m.selected = indices[len(indices)-1]
}

// scrollBoardToSelection adjusts boardScrollY so the selected card is visible.
func (m *Model) scrollBoardToSelection() {
	layout := m.computeBoard()
	if len(layout.rows) == 0 {
		m.boardScrollY = 0
		return
	}
	var docID string
	if m.selected >= 0 && m.selected < len(m.filtered) {
		docID = m.filtered[m.selected].document.ID
	}
	span, ok := layout.cardSpans[docID]
	if !ok {
		return
	}
	cardTop, cardHeight := span[0], span[1]
	visible := m.visibleRows()
	if cardTop < m.boardScrollY {
		m.boardScrollY = cardTop
	}
	if cardTop+cardHeight > m.boardScrollY+visible {
		m.boardScrollY = cardTop + cardHeight - visible
	}
	if m.boardScrollY < 0 {
		m.boardScrollY = 0
	}
	if maxScroll := len(layout.rows) - visible; maxScroll >= 0 && m.boardScrollY > maxScroll {
		m.boardScrollY = maxScroll
	}
}

func (m Model) visibleRows() int {
	// Reserve the exact number of rows rendered below the tree. The details
	// pane is one row taller when a document is selected (two fields plus its
	// top border); failing to account for that extra row makes the terminal
	// clip the first tree row, which is often the pinned row.
	reserved := 1 // status bar
	if m.inputVisible {
		reserved += 2 // input content and top border
		if m.filterCount() > 0 {
			reserved++ // filter tags
		}
		if m.cmdMenuVisible {
			reserved += m.commandMenuRows()
		}
	}
	if m.detailsVisible {
		reserved += 2 // empty details content and top border
		if _, ok := m.selectedDocument(); ok {
			reserved++ // selected-document metadata uses a second content row
		}
	}
	if m.deleteConfirm {
		reserved += 3
	}
	if m.webQuitPrompt {
		reserved += 8
	}
	if m.configVisible {
		reserved += m.configPanelRows()
	}
	return max(1, m.height-reserved)
}

func (m Model) configPanelRows() int {
	if !m.configVisible {
		return 0
	}
	// title + web section + one row per setting + hint + top border
	return m.webPanelRows() + len(m.settings) + 3
}

func (m Model) pinnedCount() int {
	count := 0
	for count < len(m.filtered) && m.filtered[count].document.Pinned {
		count++
	}
	return count
}

func (m *Model) keepSelectionVisible() {
	visible := m.visibleRows()
	pinned := m.pinnedCount()
	if pinned == 0 {
		if m.selected < m.scrollTop {
			m.scrollTop = m.selected
		}
		if m.selected >= m.scrollTop+visible {
			m.scrollTop = m.selected - visible + 1
		}
		m.scrollTop = min(max(0, m.scrollTop), max(0, len(m.filtered)-visible))
		return
	}
	// Pinned rows own the top of the tree viewport. scrollTop addresses only
	// the unpinned region, whose height changes when details/input rows appear.
	if m.selected < pinned {
		return
	}
	unpinnedVisible := max(0, visible-min(pinned, visible))
	if unpinnedVisible == 0 {
		m.scrollTop = pinned
		return
	}
	start := max(pinned, m.scrollTop)
	if m.selected < start {
		start = m.selected
	}
	if m.selected >= start+unpinnedVisible {
		start = m.selected - unpinnedVisible + 1
	}
	maxStart := max(pinned, len(m.filtered)-unpinnedVisible)
	m.scrollTop = min(max(pinned, start), maxStart)
}

func (m *Model) openInput(mode string) tea.Cmd {
	m.inputVisible = true
	m.inputActive = true
	m.inputMode = mode
	m.input.Placeholder = inputPlaceholder(mode)
	m.input.Focus()
	m.input.SetValue("")
	m.historyIndex = len(m.commandHistory)
	m.cmdSuggestions = nil
	m.cmdSelected = 0
	m.cmdMenuVisible = false
	m.filterErr = nil
	m.keepSelectionVisible()
	if mode == inputModeAgent {
		// Ensure Companion + Agent session while focusing the composer.
		m.agent.sequence++
		m.agent.badge = "CONNECTING"
		m.agent.err = nil
		return tea.Batch(textinput.Blink, ensureAgentClientCmd(m.ctx, m.app, m.agent.clientID, m.agent.sequence))
	}
	return textinput.Blink
}

func (m *Model) toggleInput() tea.Cmd {
	if m.inputVisible {
		m.hideInput()
		return m.filterChanged(nil)
	}
	return m.openInput(m.inputMode)
}

func (m *Model) cycleMediaScope() tea.Cmd {
	previousID := ""
	if document, ok := m.selectedDocument(); ok {
		previousID = document.ID
	}
	switch m.mediaScope {
	case mediaScopeAll:
		m.mediaScope = mediaScopeMarkdown
	case mediaScopeMarkdown:
		m.mediaScope = mediaScopePDF
	case mediaScopePDF:
		m.mediaScope = mediaScopeImage
	default:
		m.mediaScope = mediaScopeAll
	}
	m.statusMessage = "showing " + mediaScopeLabel(m.mediaScope)
	command := m.filterChanged(nil)
	m.restoreSelection(previousID)
	return command
}

func (m *Model) hideInput() {
	m.inputVisible = false
	m.inputActive = false
	m.cmdSuggestions = nil
	m.cmdSelected = 0
	m.cmdMenuVisible = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *Model) clearExecutedCommand() {
	command := strings.TrimSpace(m.input.Value())
	if command != "" && (len(m.commandHistory) == 0 || m.commandHistory[len(m.commandHistory)-1] != command) {
		m.commandHistory = append(m.commandHistory, command)
		if len(m.commandHistory) > 50 {
			m.commandHistory = m.commandHistory[len(m.commandHistory)-50:]
		}
	}
	m.input.SetValue("")
	m.input.CursorEnd()
	m.cmdSuggestions = nil
	m.cmdSelected = 0
	m.cmdMenuVisible = false
	m.historyIndex = len(m.commandHistory)
}

func (m *Model) commitFilterToken() (bool, error) {
	value := strings.TrimRight(m.input.Value(), " \t")
	separator := strings.LastIndexAny(value, " \t")
	token := value
	remaining := ""
	if separator >= 0 {
		token = value[separator+1:]
		remaining = strings.TrimSpace(value[:separator])
	}
	remainingInput := remaining
	if remainingInput != "" {
		remainingInput += " "
	}
	if token == "/clear" {
		m.dateFilters = nil
		m.textFilters = nil
		m.input.SetValue(remainingInput)
		return true, nil
	}
	if strings.HasPrefix(token, "/") {
		return true, fmt.Errorf("unknown filter command %q", token)
	}
	if !strings.HasPrefix(token, "+") {
		return false, nil
	}
	filter, err := parseDateFilter(token, time.Now())
	if err != nil {
		return true, err
	}
	for _, existing := range m.dateFilters {
		if existing.Label == filter.Label {
			m.input.SetValue(remainingInput)
			return true, nil
		}
	}
	filter.Sequence = m.nextFilterSequence()
	m.dateFilters = append(m.dateFilters, filter)
	m.input.SetValue(remainingInput)
	return true, nil
}

func (m *Model) commitTextFilter() bool {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return false
	}
	for _, existing := range m.textFilters {
		if existing.Mode == m.searchMode && strings.EqualFold(existing.Value, value) {
			m.input.SetValue("")
			return true
		}
	}
	m.textFilters = append(m.textFilters, textFilter{
		Value:    value,
		Mode:     m.searchMode,
		Sequence: m.nextFilterSequence(),
		Exact:    m.filterExact,
		Case:     m.filterCase,
	})
	m.input.SetValue("")
	return true
}

func (m *Model) nextFilterSequence() uint64 {
	m.filterSequence++
	return m.filterSequence
}

func (m Model) filterCount() int { return len(m.dateFilters) + len(m.textFilters) }

func (m *Model) removeLastFilter() bool {
	if m.filterCount() == 0 {
		return false
	}
	dateSequence, textSequence := uint64(0), uint64(0)
	if len(m.dateFilters) > 0 {
		dateSequence = m.dateFilters[len(m.dateFilters)-1].Sequence
	}
	if len(m.textFilters) > 0 {
		textSequence = m.textFilters[len(m.textFilters)-1].Sequence
	}
	if len(m.textFilters) > 0 && (len(m.dateFilters) == 0 || textSequence >= dateSequence) {
		m.textFilters = m.textFilters[:len(m.textFilters)-1]
	} else {
		m.dateFilters = m.dateFilters[:len(m.dateFilters)-1]
	}
	return true
}

// effectiveNameFilters returns the name-mode filters that should apply right
// now: committed filters (semantics frozen at commit time) plus the live draft
// (which follows the current match/case settings from the options panel).
func (m Model) effectiveNameFilters() []textFilter {
	filters := make([]textFilter, 0, len(m.textFilters)+1)
	for _, filter := range m.textFilters {
		if filter.Mode == searchModeName {
			filters = append(filters, filter)
		}
	}
	if m.inputVisible && m.searchMode == searchModeName {
		if draft := textFilterQuery(m.input.Value()); draft != "" {
			filters = append(filters, textFilter{
				Value:    draft,
				Mode:     searchModeName,
				Sequence: m.nextFilterSequence(),
				Exact:    m.filterExact,
				Case:     m.filterCase,
			})
		}
	}
	return filters
}

// fullTextFilter returns the combined content-search query plus whether every
// contributing filter wants exact (whole-token) matching. Committed filters
// freeze their own semantics; the live draft follows the options panel.
func (m Model) fullTextFilter() (string, bool) {
	queries := make([]string, 0, len(m.textFilters)+1)
	exact := true
	for _, filter := range m.textFilters {
		if filter.Mode == searchModeFull {
			queries = append(queries, filter.Value)
			if !filter.Exact {
				exact = false
			}
		}
	}
	if m.inputVisible && m.searchMode == searchModeFull {
		if draft := textFilterQuery(m.input.Value()); draft != "" {
			queries = append(queries, draft)
			if !m.filterExact {
				exact = false
			}
		}
	}
	return strings.Join(queries, " "), exact
}

func (m Model) fullTextFilterQuery() string {
	query, _ := m.fullTextFilter()
	return query
}

func (m *Model) filterChanged(inputCommand tea.Cmd) tea.Cmd {
	m.filterErr = nil
	m.applyPreviewContent()
	if query, exact := m.fullTextFilter(); query != "" {
		m.loading = true
		commands := []tea.Cmd{inputCommand, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, query, exact)}
		// The visible thread filters by the same full-text query: body hits
		// arrive via threadSearchMsg and intersect the linked cards.
		if m.graphFocusID != "" {
			commands = append(commands, m.threadSearchCmd(query, exact))
		}
		return tea.Batch(commands...)
	}
	m.graphSearchQuery, m.graphSearchHits = "", nil
	m.loading = false
	m.refreshFilter()
	return tea.Batch(inputCommand, m.loadPreview())
}

func (m *Model) refreshFilter() {
	nameFilters := m.effectiveNameFilters()
	m.filtered = m.filtered[:0]
	for _, candidate := range m.items {
		if m.hideNotes && isClippedNote(candidate.filename) {
			continue
		}
		if matchesMediaScope(candidate.document, m.mediaScope) && matchesTextFilters(candidate.title, candidate.filename, candidate.match, nameFilters) && matchesDateFilters(candidate.document, m.dateFilters) {
			m.filtered = append(m.filtered, candidate)
		}
	}
	m.sortFiltered()
	if len(m.filtered) == 0 {
		m.selected = 0
		m.scrollTop = 0
	} else if m.selected >= len(m.filtered) {
		m.selected = len(m.filtered) - 1
	}
	m.keepSelectionVisible()
	// A live thread view shares the same filters: keep its selection inside
	// the newly visible cards.
	if m.graphFocusID != "" {
		if visible := len(m.visibleGraphIndices()); visible > 0 && m.graphSelected >= visible {
			m.graphSelected = visible - 1
		}
		m.scrollGraphToSelection()
	}
}

// startScan runs path scan then reloads the document tree (ctrl+r).
func (m *Model) startScan() tea.Cmd {
	m.loading = true
	m.err = nil
	m.statusMessage = "scanning…"
	m.deleteConfirm = false
	return tea.Batch(m.spinner.Tick, scanCmd(m.ctx, m.app))
}

// removeDeletedDocument updates the local tree immediately after a successful
// delete. refreshFilter clamps the old numeric selection: normally that lands
// on the next row, while deleting the last row lands on its previous neighbor.
func (m *Model) removeDeletedDocument(documentID string) {
	if documentID == "" {
		return
	}
	items := m.items[:0]
	for _, candidate := range m.items {
		if candidate.document.ID != documentID {
			items = append(items, candidate)
		}
	}
	m.items = items
	m.refreshFilter()
}

func (m *Model) restoreSelection(documentID string) {
	if len(m.filtered) == 0 {
		m.selected = 0
		m.scrollTop = 0
		return
	}
	if documentID != "" {
		for index, candidate := range m.filtered {
			if candidate.document.ID == documentID {
				m.selected = index
				m.keepSelectionVisible()
				return
			}
		}
	}
	m.selected = 0
	m.scrollTop = 0
	m.keepSelectionVisible()
}

func formatScanStatus(report membox.ScanReport) string {
	return fmt.Sprintf("scan +%d ~%d missing=%d files=%d", report.Added, report.Updated, report.Missing, report.Files)
}

func (m *Model) applyDocumentTitle(documentID, title string) {
	previousID := ""
	if selected, ok := m.selectedDocument(); ok {
		previousID = selected.ID
	}
	for index := range m.items {
		if m.items[index].document.ID != documentID {
			continue
		}
		m.items[index].document.Title = title
		m.items[index] = documentItems([]membox.DocumentView{m.items[index].document})[0]
		break
	}
	m.refreshFilter()
	m.restoreSelection(previousID)
}

func (m *Model) applyPinnedState(documentID string, pinned bool) {
	for index := range m.items {
		if m.items[index].document.ID == documentID {
			m.items[index].document.Pinned = pinned
			break
		}
	}
	for index := range m.filtered {
		if m.filtered[index].document.ID == documentID {
			m.filtered[index].document.Pinned = pinned
			break
		}
	}
	m.sortFiltered()
	for index, candidate := range m.filtered {
		if candidate.document.ID == documentID {
			m.selected = index
			break
		}
	}
	if pinned {
		m.scrollTop = 0
	}
	m.keepSelectionVisible()
	if m.viewMode == viewBoard {
		m.scrollBoardToSelection()
	}
}

func (m *Model) sortFiltered() {
	byName := func(left, right item) bool {
		leftName, rightName := strings.ToLower(treeItemLabel(left)), strings.ToLower(treeItemLabel(right))
		if leftName != rightName {
			return leftName < rightName
		}
		leftPath, rightPath := strings.ToLower(left.document.Path), strings.ToLower(right.document.Path)
		if leftPath != rightPath {
			return leftPath < rightPath
		}
		return left.document.ID < right.document.ID
	}
	sort.SliceStable(m.filtered, func(i, j int) bool {
		left, right := m.filtered[i], m.filtered[j]
		if left.document.Pinned != right.document.Pinned {
			return left.document.Pinned
		}
		// The tree is always newest-first; alphabetical sorting was removed
		// along with the old `s` toggle.
		if !left.document.UpdatedAt.Equal(right.document.UpdatedAt) {
			return left.document.UpdatedAt.After(right.document.UpdatedAt)
		}
		return byName(left, right)
	})
}

// textFilterQuery removes an in-progress command token from the end of the
// input. Command tags only affect results after Space or Enter commits them, so
// a draft such as +m:7d must not temporarily empty filename or FTS results.
func textFilterQuery(value string) string {
	value = strings.TrimSpace(value)
	separator := strings.LastIndexAny(value, " \t")
	token := value
	if separator >= 0 {
		token = value[separator+1:]
	}
	if strings.HasPrefix(token, "+") || strings.HasPrefix(token, "/") {
		if separator < 0 {
			return ""
		}
		return strings.TrimSpace(value[:separator])
	}
	return value
}

func matchesTextFilters(title, filename, match string, filters []textFilter) bool {
	for _, filter := range filters {
		if !matchNameFilter(title, filename, match, filter) {
			return false
		}
	}
	return true
}

// matchNameFilter applies one name-mode filter. Word (全匹配) requires the
// query to occur as a whole word (word chars = [A-Za-z0-9_]; CJK and
// punctuation act as boundaries, so Chinese substrings still match):
// "rust" hits "The Rust I Wanted…" but not the "Trust" inside
// "Don't Trust the Agent". Case keeps the original casing.
func matchNameFilter(title, filename, match string, filter textFilter) bool {
	query := strings.TrimSpace(filter.Value)
	if query == "" {
		return true
	}
	if filter.Exact {
		// Every query word must appear as a whole word somewhere in the
		// title/path/filename/status/id haystack (space-joined as `match`).
		for _, word := range strings.Fields(query) {
			if !wholeWordMatch(match, word, filter.Case) {
				return false
			}
		}
		return true
	}
	if filter.Case {
		return wordsMatchCase(title, query) || wordsMatchCase(filename, query) || wordsMatchCase(match, query)
	}
	return wordsMatch(title+" "+filename+" "+match, strings.ToLower(query))
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// wholeWordMatch reports whether needle occurs in haystack as a whole word:
// the characters right before and after the occurrence must not be word chars.
// CJK bytes are never word chars, so Chinese runs behave as boundaries and
// CJK substrings match whole-word style (no spaces to split on).
func wholeWordMatch(haystack, needle string, caseSensitive bool) bool {
	if needle == "" {
		return true
	}
	if !caseSensitive {
		haystack = strings.ToLower(haystack)
		needle = strings.ToLower(needle)
	}
	for i := 0; ; {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		pos := i + j
		before := pos == 0 || !isWordChar(rune(haystack[pos-1]))
		after := pos+len(needle) >= len(haystack) || !isWordChar(rune(haystack[pos+len(needle)]))
		if before && after {
			return true
		}
		i = pos + 1
	}
}

func wordsMatch(value, query string) bool {
	value = strings.ToLower(value)
	for _, word := range strings.Fields(query) {
		if !strings.Contains(value, word) {
			return false
		}
	}
	return true
}

// wordsMatchCase is the case-sensitive twin of wordsMatch.
func wordsMatchCase(value, query string) bool {
	for _, word := range strings.Fields(query) {
		if !strings.Contains(value, word) {
			return false
		}
	}
	return true
}

func searchDocumentsCmd(ctx context.Context, app App, query string, exact bool) tea.Cmd {
	return func() tea.Msg {
		results, err := app.SearchDocuments(ctx, membox.SearchDocumentsQuery{Query: query, Limit: 100, Exact: exact})
		return searchMsg{query: strings.TrimSpace(query), exact: exact, results: results, err: err}
	}
}
func listDocumentsCmd(ctx context.Context, app App, sequence uint64) tea.Cmd {
	return func() tea.Msg {
		documents, err := app.ListDocuments(ctx, membox.ListDocumentsQuery{Limit: 1000})
		return documentsMsg{sequence: sequence, documents: documents, err: err}
	}
}
func previewCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		location, err := app.ResolveDocumentLocation(ctx, membox.ResolveLocationQuery{Selector: selector})
		if err != nil {
			return previewMsg{documentID: selector, err: err}
		}
		if strings.EqualFold(filepath.Ext(location.Path), ".pdf") {
			return previewMsg{documentID: selector, content: "PDF document\n\n" + location.Path + "\n\nPress Enter to open with the system viewer."}
		}
		body, err := app.ReadDocument(ctx, membox.ReadDocumentQuery{Selector: selector})
		return previewMsg{documentID: selector, content: string(body), err: err}
	}
}
func scanCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg {
		report, err := app.ScanPaths(ctx, membox.ScanPathsCommand{})
		return scanMsg{report: report, err: err}
	}
}
func resolveViewerCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		location, err := app.ResolveDocumentLocation(ctx, membox.ResolveLocationQuery{Selector: selector})
		if err == nil && location.Status != "active" {
			err = fmt.Errorf("document %s is %s at %s", location.DocumentID, location.Status, location.Path)
		}
		return editReadyMsg{selector: selector, path: location.Path, viewer: true, err: err}
	}
}
func resolveEditorCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		location, err := app.ResolveDocumentLocation(ctx, membox.ResolveLocationQuery{Selector: selector})
		if err == nil && location.Status != "active" {
			err = fmt.Errorf("document %s is %s at %s", location.DocumentID, location.Status, location.Path)
		}
		return editReadyMsg{selector: selector, path: location.Path, err: err}
	}
}
func reindexCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		return reindexMsg{err: app.ReindexDocument(ctx, membox.ReindexDocumentCommand{Selector: selector})}
	}
}
func togglePinCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		result, err := app.ToggleDocumentPin(ctx, membox.ToggleDocumentPinCommand{Selector: selector})
		return pinMsg{documentID: result.DocumentID, pinned: result.Pinned, err: err}
	}
}

func summarizeCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		view, err := app.SummarizeDocument(ctx, selector)
		return summarizeDoneMsg{documentID: selector, document: view, err: err}
	}
}

// applySummary writes a freshly generated summary into the item copies held
// by the tree and the filtered view.
func (m *Model) applySummary(documentID, summary string) {
	for index := range m.items {
		if m.items[index].document.ID == documentID {
			m.items[index].document.Summary = summary
			break
		}
	}
	for index := range m.filtered {
		if m.filtered[index].document.ID == documentID {
			m.filtered[index].document.Summary = summary
			break
		}
	}
}

func deleteDocumentCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		result, err := app.DeleteDocument(ctx, membox.DeleteDocumentCommand{Selector: selector})
		msg := deleteResultMsg{documentID: result.DocumentID, path: result.Path, err: err}
		if err == nil {
			if summary, summaryErr := app.TrashSummary(ctx); summaryErr == nil {
				msg.trashCount, msg.trashBytes = summary.Count, summary.Bytes
			}
		}
		return msg
	}
}

// formatBytesTUI renders a byte count for status-line hints.
func formatBytesTUI(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
func viewerModeCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg {
		mode, err := app.GetViewer(ctx)
		return viewerModeMsg{mode: mode, err: err}
	}
}
func settingsCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg {
		settings, err := app.ListSettings(ctx)
		return settingsMsg{settings: settings, err: err}
	}
}
func setSettingCmd(ctx context.Context, app App, key, value string) tea.Cmd {
	return func() tea.Msg {
		err := app.SetSetting(ctx, key, value)
		return settingSavedMsg{key: key, value: value, err: err}
	}
}
func openDocumentWebCmd(ctx context.Context, app App, launcher host.Launcher, selector string) tea.Cmd {
	return func() tea.Msg {
		url, err := app.OpenDocumentWeb(ctx, selector)
		if err != nil {
			return openWebMsg{err: err}
		}
		command, err := launcher.OpenCommand(ctx, url)
		if err == nil {
			err = command.Run()
		}
		return openWebMsg{url: url, err: err}
	}
}
func openCmd(ctx context.Context, app App, launcher host.Launcher, selector string) tea.Cmd {
	return func() tea.Msg {
		location, err := app.ResolveDocumentLocation(ctx, membox.ResolveLocationQuery{Selector: selector})
		if err != nil {
			return openMsg{err: err}
		}
		command, err := launcher.OpenCommand(ctx, location.Path)
		if err == nil {
			err = command.Run()
		}
		return openMsg{err: err}
	}
}
