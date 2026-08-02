package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"membox"
	"membox/internal/interfaces/host"
)

const doubleSpaceWindow = 320 * time.Millisecond

const (
	searchModeName = "name"
	searchModeFull = "full"
)

const (
	viewTree  = "tree"
	viewBoard = "board"
)

const (
	sortModeName = "name"
	sortModeTime = "time"
)

type App interface {
	AddPath(context.Context, membox.AddPathCommand) (membox.AddPathResult, error)
	SearchDocuments(context.Context, membox.SearchDocumentsQuery) ([]membox.SearchResult, error)
	ListDocuments(context.Context, membox.ListDocumentsQuery) ([]membox.DocumentView, error)
	ReadDocument(context.Context, membox.ReadDocumentQuery) ([]byte, error)
	ResolveDocumentLocation(context.Context, membox.ResolveLocationQuery) (membox.LocationView, error)
	ReindexDocument(context.Context, membox.ReindexDocumentCommand) error
	ScanPaths(context.Context, membox.ScanPathsCommand) (membox.ScanReport, error)
	ListPaths(context.Context) ([]membox.PathView, error)
	GetIndexStatus(context.Context) (membox.IndexStatusView, error)
}

type item struct {
	document membox.DocumentView
	title    string
	filename string
	match    string
}

type textFilter struct {
	Value    string
	Mode     string
	Sequence uint64
}

type Model struct {
	ctx      context.Context
	app      App
	launcher host.Launcher

	input   textinput.Model
	preview viewport.Model
	spinner spinner.Model

	items          []item
	filtered       []item
	dateFilters    []dateFilter
	textFilters    []textFilter
	filterSequence uint64
	selected       int
	scrollTop      int
	boardScrollY   int
	rawContent     string
	rawDocumentID  string

	inputVisible   bool
	inputActive    bool
	detailsVisible bool
	fullscreen     bool
	fullDocument   *membox.DocumentView

	loading       bool
	err           error
	filterErr     error
	width, height int
	listSequence  uint64
	spaceSequence uint64
	lastKeyAt     time.Time
	searchMode    string
	viewMode      string
	sortMode      string
}

type searchMsg struct {
	query   string
	results []membox.SearchResult
	err     error
}

type documentsMsg struct {
	sequence  uint64
	documents []membox.DocumentView
	err       error
}
type previewMsg struct {
	documentID string
	content    string
	err        error
}
type scanMsg struct {
	report membox.ScanReport
	err    error
}
type editReadyMsg struct {
	selector string
	path     string
	viewer   bool
	err      error
}
type editorDoneMsg struct {
	selector string
	viewer   bool
	err      error
}
type reindexMsg struct{ err error }
type openMsg struct{ err error }
type spaceTimeoutMsg struct{ sequence uint64 }

func New(ctx context.Context, app App, launcher host.Launcher) Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "filter documents"
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	vp := viewport.New(40, 10)
	model := Model{ctx: ctx, app: app, launcher: launcher, input: input, spinner: spin, preview: vp, searchMode: searchModeName, viewMode: viewTree, sortMode: sortModeName}
	model.preview.SetContent(previewPlaceholder("Loading documents…"))
	return model
}

func Run(ctx context.Context, app App, launcher host.Launcher, programOptions ...tea.ProgramOption) error {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	options = append(options, programOptions...)
	_, err := tea.NewProgram(New(ctx, app, launcher), options...).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick, listDocumentsCmd(m.ctx, m.app, m.listSequence))
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
	case tea.KeyMsg:
		if msg.String() == "ctrl+d" {
			return m, tea.Quit
		}
		if m.fullscreen {
			return m.updateFullscreen(msg)
		}
		if m.inputActive {
			return m.updateFilterInput(msg)
		}
		return m.updateNavigation(msg)
	case searchMsg:
		if m.fullTextFilterQuery() == msg.query {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.filtered = searchResultItems(m.items, msg.results, m.dateFilters, m.nameTextFilterQueries())
				m.sortFiltered()
				m.selected = 0
				m.keepSelectionVisible()
				m.applyPreviewContent()
				commands = append(commands, m.loadPreview())
			}
		}
	case documentsMsg:
		if msg.sequence == m.listSequence {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.items = documentItems(msg.documents)
				m.selected = 0
				m.scrollTop = 0
				m.refreshFilter()
				if query := m.fullTextFilterQuery(); query != "" {
					m.loading = true
					commands = append(commands, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, query))
				} else {
					commands = append(commands, m.loadPreview())
				}
			}
		}
	case previewMsg:
		m.err = msg.err
		if msg.err == nil {
			m.rawContent = msg.content
			if msg.documentID != "" {
				m.rawDocumentID = msg.documentID
			} else if document, ok := m.selectedDocument(); ok {
				m.rawDocumentID = document.ID
			}
			m.applyPreviewContent()
		}
	case scanMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
	case editReadyMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			var command *exec.Cmd
			var err error
			if msg.viewer {
				command, err = m.launcher.ViewerCommand(m.ctx, msg.path)
			} else {
				command, err = m.launcher.EditorCommand(m.ctx, msg.path)
			}
			if err != nil {
				m.err = err
				break
			}
			return m, tea.ExecProcess(command, func(err error) tea.Msg { return editorDoneMsg{selector: msg.selector, viewer: msg.viewer, err: err} })
		}
	case editorDoneMsg:
		m.err = msg.err
		if msg.err == nil && !msg.viewer {
			m.loading = true
			commands = append(commands, m.spinner.Tick, reindexCmd(m.ctx, m.app, msg.selector))
		}
	case reindexMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			commands = append(commands, m.loadPreview())
		}
	case openMsg:
		m.err = msg.err
	case spaceTimeoutMsg:
		if msg.sequence == m.spaceSequence {
			m.spaceSequence = 0
			m.lastKeyAt = time.Time{}
			m.detailsVisible = !m.detailsVisible
			m.keepSelectionVisible()
		}
	case spinner.TickMsg:
		if m.loading {
			var command tea.Cmd
			m.spinner, command = m.spinner.Update(msg)
			commands = append(commands, command)
		}
	}
	var command tea.Cmd
	m.preview, command = m.preview.Update(message)
	commands = append(commands, command)
	return m, tea.Batch(commands...)
}

func (m Model) updateFullscreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.fullscreen = false
		m.fullDocument = nil
		return m, m.loadPreview()
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

func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C deletes a character in the input box (it no longer quits; Ctrl+D quits).
	if msg.String() == "ctrl+c" {
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	}
	switch msg.String() {
	case "esc":
		m.filterErr = nil
		m.hideInput()
		return m, m.filterChanged(nil)
	case "tab":
		if m.searchMode == searchModeName {
			m.searchMode = searchModeFull
		} else {
			m.searchMode = searchModeName
		}
		return m, m.filterChanged(nil)
	case "ctrl+g":
		m.detailsVisible = !m.detailsVisible
		m.keepSelectionVisible()
		return m, nil
	case "enter":
		handled, err := m.commitFilterToken()
		if handled {
			m.filterErr = err
			if err != nil {
				return m, nil
			}
			return m, m.filterChanged(nil)
		}
		if m.commitTextFilter() {
			return m, m.filterChanged(nil)
		}
		m.hideInput()
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
		}
		return m, nil
	case "up", "down", "pgup", "pgdown":
		return m.moveSelection(msg.String())
	case "ctrl+u":
		m.input.SetValue("")
		return m, m.filterChanged(nil)
	case "backspace":
		if m.input.Value() == "" && m.removeLastFilter() {
			return m, m.filterChanged(nil)
		}
	case "space", " ":
		handled, err := m.commitFilterToken()
		if handled {
			m.filterErr = err
			if err != nil {
				return m, nil
			}
			return m, m.filterChanged(nil)
		}
	}

	m.spaceSequence = 0
	m.lastKeyAt = time.Time{}
	m.filterErr = nil
	var command tea.Cmd
	m.input, command = m.input.Update(msg)
	return m, m.filterChanged(command)
}

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg.String() {
	case "q":
		// Contextual "back" within the TUI; Ctrl+D quits the program.
		if m.viewMode == viewBoard {
			m.viewMode = viewTree
			m.keepSelectionVisible()
		} else if m.detailsVisible {
			m.detailsVisible = false
		}
		return m, nil
	case "enter":
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			commands = append(commands, m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
		}
	case "space", " ":
		if m.lastKeyAt.Add(doubleSpaceWindow).After(time.Now()) {
			// double space confirmed: toggle input
			m.spaceSequence++
			m.lastKeyAt = time.Time{}
			commands = append(commands, m.toggleInput())
			return m, tea.Batch(commands...)
		}
		// first space: schedule delayed details toggle
		m.lastKeyAt = time.Now()
		m.spaceSequence++
		sequence := m.spaceSequence
		commands = append(commands, tea.Tick(doubleSpaceWindow, func(time.Time) tea.Msg { return spaceTimeoutMsg{sequence: sequence} }))
	case "tab":
		// Toggle between tree and board view when input is not active
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
		if msg.String() == "k" {
			return m.moveSelection("up")
		}
		if msg.String() == "j" {
			return m.moveSelection("down")
		}
		return m.moveSelection(msg.String())
	case "s":
		m.toggleSort()
		return m, m.loadPreview()
	case "r":
		m.loading = true
		commands = append(commands, m.spinner.Tick, scanCmd(m.ctx, m.app))
	case "e":
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			commands = append(commands, m.spinner.Tick, resolveEditorCmd(m.ctx, m.app, document.ID))
		}
	case "o":
		if document, ok := m.selectedDocument(); ok {
			commands = append(commands, openCmd(m.ctx, m.app, m.launcher, document.ID))
		}
	default:
		m.spaceSequence = 0
		m.lastKeyAt = time.Time{}
	}
	return m, tea.Batch(commands...)
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
	if m.inputVisible {
		tagRows := 0
		if m.filterCount() > 0 {
			tagRows = 1
		}
		if m.detailsVisible {
			return max(3, m.height-5-tagRows)
		}
		return max(3, m.height-3-tagRows)
	}
	if m.detailsVisible {
		return max(3, m.height-3)
	}
	return max(3, m.height-1)
}

func (m *Model) keepSelectionVisible() {
	visible := m.visibleRows()
	if m.selected < m.scrollTop {
		m.scrollTop = m.selected
	}
	if m.selected >= m.scrollTop+visible {
		m.scrollTop = m.selected - visible + 1
	}
	m.scrollTop = min(max(0, m.scrollTop), max(0, len(m.filtered)-visible))
}

func (m *Model) toggleInput() tea.Cmd {
	if m.inputVisible {
		m.hideInput()
		return m.filterChanged(nil)
	}
	m.inputVisible = true
	m.inputActive = true
	m.input.Focus()
	m.input.SetValue("")
	m.keepSelectionVisible()
	return textinput.Blink
}

func (m *Model) hideInput() {
	m.inputVisible = false
	m.inputActive = false
	m.input.Blur()
	m.input.SetValue("")
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
	m.textFilters = append(m.textFilters, textFilter{Value: value, Mode: m.searchMode, Sequence: m.nextFilterSequence()})
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

func (m Model) nameTextFilterQueries() []string {
	queries := make([]string, 0, len(m.textFilters)+1)
	for _, filter := range m.textFilters {
		if filter.Mode == searchModeName {
			queries = append(queries, filter.Value)
		}
	}
	if m.inputVisible && m.searchMode == searchModeName {
		if draft := textFilterQuery(m.input.Value()); draft != "" {
			queries = append(queries, draft)
		}
	}
	return queries
}

func (m Model) fullTextFilterQuery() string {
	queries := make([]string, 0, len(m.textFilters)+1)
	for _, filter := range m.textFilters {
		if filter.Mode == searchModeFull {
			queries = append(queries, filter.Value)
		}
	}
	if m.inputVisible && m.searchMode == searchModeFull {
		if draft := textFilterQuery(m.input.Value()); draft != "" {
			queries = append(queries, draft)
		}
	}
	return strings.Join(queries, " ")
}

func (m *Model) filterChanged(inputCommand tea.Cmd) tea.Cmd {
	m.filterErr = nil
	m.applyPreviewContent()
	if query := m.fullTextFilterQuery(); query != "" {
		m.loading = true
		return tea.Batch(inputCommand, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, query))
	}
	m.loading = false
	m.refreshFilter()
	return tea.Batch(inputCommand, m.loadPreview())
}

func (m *Model) refreshFilter() {
	nameQueries := m.nameTextFilterQueries()
	m.filtered = m.filtered[:0]
	for _, candidate := range m.items {
		if matchesTextFilters(candidate.match, nameQueries) && matchesDateFilters(candidate.document, m.dateFilters) {
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
}

func (m *Model) toggleSort() {
	selectedID := ""
	if document, ok := m.selectedDocument(); ok {
		selectedID = document.ID
	}
	if m.sortMode == sortModeTime {
		m.sortMode = sortModeName
	} else {
		m.sortMode = sortModeTime
	}
	m.sortFiltered()
	for index, candidate := range m.filtered {
		if candidate.document.ID == selectedID {
			m.selected = index
			break
		}
	}
	m.keepSelectionVisible()
	if m.viewMode == viewBoard {
		m.snapToBoardSelection()
		m.scrollBoardToSelection()
	}
}

func (m *Model) sortFiltered() {
	byName := func(left, right item) bool {
		leftName, rightName := strings.ToLower(left.filename), strings.ToLower(right.filename)
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
		if m.sortMode == sortModeTime && !left.document.UpdatedAt.Equal(right.document.UpdatedAt) {
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

func matchesTextFilters(value string, queries []string) bool {
	for _, query := range queries {
		if !wordsMatch(value, strings.ToLower(query)) {
			return false
		}
	}
	return true
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

func (m Model) selectedDocument() (membox.DocumentView, bool) {
	if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
		return membox.DocumentView{}, false
	}
	return m.filtered[m.selected].document, true
}

func (m Model) loadPreview() tea.Cmd {
	if m.fullscreen && m.fullDocument != nil {
		return previewCmd(m.ctx, m.app, m.fullDocument.ID)
	}
	if document, ok := m.selectedDocument(); ok {
		selector := document.ID
		return func() tea.Msg {
			body, err := m.app.ReadDocument(m.ctx, membox.ReadDocumentQuery{Selector: selector})
			return previewMsg{documentID: selector, content: string(body), err: err}
		}
	}
	return func() tea.Msg { return previewMsg{content: previewPlaceholder("No document selected.")} }
}

func (m *Model) applyPreviewContent() {
	query := ""
	if m.inputVisible {
		query = textFilterQuery(m.input.Value())
	}
	if query == "" && len(m.textFilters) > 0 {
		query = m.textFilters[len(m.textFilters)-1].Value
	}
	content := highlightQuery(m.rawContent, query)
	m.preview.SetContent(content)
	if line := firstMatchLine(m.rawContent, query); line >= 0 {
		target := max(0, line-m.preview.Height/2)
		m.preview.SetYOffset(target)
		return
	}
	m.preview.GotoTop()
}

func highlightQuery(content, query string) string {
	if query == "" || content == "" {
		return content
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#111111", Light: "#111111"}).Background(colors.HighlightBG).Bold(true)
	parts := strings.Split(content, "\n")
	lowerQuery := strings.ToLower(query)
	for i, line := range parts {
		var highlighted strings.Builder
		remaining := line
		for {
			index := strings.Index(strings.ToLower(remaining), lowerQuery)
			if index < 0 {
				highlighted.WriteString(remaining)
				break
			}
			highlighted.WriteString(remaining[:index])
			highlighted.WriteString(style.Render(remaining[index : index+len(query)]))
			remaining = remaining[index+len(query):]
		}
		parts[i] = highlighted.String()
	}
	return strings.Join(parts, "\n")
}

func firstMatchLine(content, query string) int {
	if query == "" {
		return -1
	}
	lowerQuery := strings.ToLower(query)
	for index, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), lowerQuery) {
			return index
		}
	}
	return -1
}

func (m *Model) resize() {
	if m.fullscreen {
		m.preview.Width = max(20, m.width-2)
		m.preview.Height = max(3, m.height-2)
	} else {
		_, previewWidth := m.layoutWidths()
		m.preview.Width = previewWidth
		m.preview.Height = m.visibleRows()
	}
	m.input.Width = max(10, m.width-12)
}

func (m Model) layoutWidths() (int, int) {
	if m.width >= 100 {
		list := max(32, m.width*2/5)
		return list, max(40, m.width-list-2)
	}
	list := max(24, m.width/2)
	return list, max(20, m.width-list-2)
}

func (m Model) View() string {
	if m.fullscreen {
		return m.fullscreenView()
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
	if m.inputVisible {
		parts = append(parts, m.inputView())
	}
	parts = append(parts, m.statusBar())
	return strings.Join(parts, "\n")
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

func (m Model) fullscreenView() string {
	header := accentStyle.Render("membox")
	if m.fullDocument != nil {
		header += dimStyle.Render("  " + shortID(m.fullDocument.ID) + "  " + displayTitle(m.fullDocument.Title, m.fullDocument.Path))
	}
	footer := dimStyle.Render(fmt.Sprintf("%3.0f%%  ↑/↓ line • pgup/pgdn page • q close", m.preview.ScrollPercent()*100))
	return header + "\n" + m.preview.View() + "\n" + footer
}

func (m Model) treePreviewView() string {
	listWidth, previewWidth := m.layoutWidths()
	visible := m.visibleRows()
	start := min(max(0, m.scrollTop), max(0, len(m.filtered)-visible))
	end := min(len(m.filtered), start+visible)
	uuidWidth := 4
	filenameWidth := max(12, listWidth-uuidWidth-8)
	if listWidth >= 72 {
		filenameWidth = max(16, listWidth-uuidWidth-30)
	} else if listWidth >= 52 {
		filenameWidth = max(14, listWidth-uuidWidth-20)
	}
	var lines []string
	for i := start; i < end; i++ {
		candidate := m.filtered[i]
		uuid := dimStyle.Render(fitWidth(shortID(candidate.document.ID), uuidWidth))
		filename := fitMiddle(candidate.filename, filenameWidth)
		dates := ""
		if listWidth >= 72 {
			created, updated := dateOnly(candidate.document.CreatedAt), dateOnly(candidate.document.UpdatedAt)
			dates = dimStyle.Render("  " + created + "  " + updated)
		} else if listWidth >= 52 {
			dates = dimStyle.Render("  " + dateOnly(candidate.document.UpdatedAt))
		}
		line := lipgloss.NewStyle().Width(listWidth - 2).MaxWidth(listWidth - 2).Inline(true).Render(uuid + "  " + filename + dates)
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
func (m Model) computeBoard() boardLayout {
	layout := boardLayout{cardSpans: map[string][2]int{}}

	var boardItems []item
	for _, candidate := range m.filtered {
		if candidate.document.Summary != "" {
			boardItems = append(boardItems, candidate)
		}
	}
	if len(boardItems) == 0 {
		return layout
	}

	const targetCardWidth = 32
	const gap = 2
	numCols := max(1, (m.width+gap)/(targetCardWidth+gap))
	cardWidth := (m.width - (numCols-1)*gap) / numCols

	type placedCard struct {
		docID string
		col   int
		row   int
		lines []string
	}
	colHeights := make([]int, numCols)
	var placed []placedCard

	var selectedID string
	if m.selected >= 0 && m.selected < len(m.filtered) {
		selectedID = m.filtered[m.selected].document.ID
	}

	for _, candidate := range boardItems {
		shortest := 0
		for c := 1; c < numCols; c++ {
			if colHeights[c] < colHeights[shortest] {
				shortest = c
			}
		}
		cardLines := m.renderCard(candidate, cardWidth, candidate.document.ID == selectedID)
		placed = append(placed, placedCard{docID: candidate.document.ID, col: shortest, row: colHeights[shortest], lines: cardLines})
		colHeights[shortest] += len(cardLines) + gap
	}

	totalHeight := 0
	for _, h := range colHeights {
		if h > totalHeight {
			totalHeight = h
		}
	}
	if totalHeight > 0 {
		totalHeight -= gap // remove trailing gap
	}

	canvas := make([][]string, totalHeight)
	for i := range canvas {
		canvas[i] = make([]string, numCols)
		for c := range canvas[i] {
			canvas[i][c] = strings.Repeat(" ", cardWidth)
		}
	}

	for _, pc := range placed {
		layout.cardSpans[pc.docID] = [2]int{pc.row, len(pc.lines)}
		for lineIdx, line := range pc.lines {
			if pc.row+lineIdx < len(canvas) {
				canvas[pc.row+lineIdx][pc.col] = line
			}
		}
	}

	for _, row := range canvas {
		layout.rows = append(layout.rows, strings.Join(row, strings.Repeat(" ", gap)))
	}
	return layout
}

// boardView renders the masonry board, offset vertically by boardScrollY so the
// selected card can be scrolled into view.
func (m Model) boardView() string {
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

// renderCard renders a single document as a Unicode box card
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
		mode := "N"
		if filter.Mode == searchModeFull {
			mode = "F"
		}
		tags = append(tags, renderedTag{sequence: filter.Sequence, value: textStyle.Render(" " + mode + ": " + filter.Value + " ")})
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
	if tags := m.tagsView(); tags != "" {
		return tags + "\n" + input
	}
	return input
}

func (m Model) statusBar() string {
	width := max(20, m.width)
	left := dimStyle.Render(m.hints())
	right := ""
	if m.loading {
		right = m.spinner.View() + " " + right
	}
	if m.filterErr != nil {
		right += errorStyle.Render("Invalid filter: " + m.filterErr.Error())
	} else if m.err != nil {
		right += errorStyle.Render("Error: " + m.err.Error())
	} else {
		if m.inputVisible {
			right += accentStyle.Render("filter") + dimStyle.Render(fmt.Sprintf(" %d/%d", len(m.filtered), len(m.items)))
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
	if m.searchMode == searchModeFull {
		mode = " FULL "
		background = lipgloss.AdaptiveColor{Dark: "#2f7d4a", Light: "#c9f0d8"}
		foreground = lipgloss.AdaptiveColor{Dark: "#f4fff8", Light: "#173f26"}
	}
	return lipgloss.NewStyle().Background(background).Foreground(foreground).Bold(true).Inline(true).Render(mode)
}

func (m Model) hints() string {
	if m.inputVisible {
		return "space date tag • enter text tag/open • backspace last • /clear • tab mode • esc hide"
	}
	sortLabel := "name"
	if m.sortMode == sortModeTime {
		sortLabel = "newest"
	}
	return "s sort:" + sortLabel + " • space details • space×2 filter • tab board • enter open • ↑↓ select • q back • ctrl+d quit"
}

func searchResultItems(items []item, results []membox.SearchResult, dateFilters []dateFilter, nameQueries []string) []item {
	allowed := make(map[string]bool, len(results))
	for _, result := range results {
		allowed[result.DocumentID] = true
	}
	filtered := make([]item, 0, len(results))
	for _, candidate := range items {
		if allowed[candidate.document.ID] && matchesDateFilters(candidate.document, dateFilters) && matchesTextFilters(candidate.match, nameQueries) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
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

func previewPlaceholder(message string) string {
	return headingStyle.Render("membox") + "\n\n" + dimStyle.Render(message)
}
func fitWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
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

func searchDocumentsCmd(ctx context.Context, app App, query string) tea.Cmd {
	return func() tea.Msg {
		results, err := app.SearchDocuments(ctx, membox.SearchDocumentsQuery{Query: query, Limit: 100})
		return searchMsg{query: strings.TrimSpace(query), results: results, err: err}
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
		body, err := app.ReadDocument(ctx, membox.ReadDocumentQuery{Selector: selector})
		return previewMsg{content: string(body), err: err}
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
