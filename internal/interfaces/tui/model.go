package tui

import (
	"context"
	"fmt"
	"path/filepath"
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
	match    string
}

type Model struct {
	ctx      context.Context
	app      App
	launcher host.Launcher

	input   textinput.Model
	preview viewport.Model
	spinner spinner.Model

	items     []item
	filtered  []item
	selected  int
	scrollTop int

	inputVisible bool
	inputActive  bool
	fullscreen   bool
	fullDocument *membox.DocumentView

	loading       bool
	err           error
	width, height int
	listSequence  uint64
	spaceSequence uint64
	lastKeyAt     time.Time
	searchMode    string
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
	content string
	err     error
}
type scanMsg struct {
	report membox.ScanReport
	err    error
}
type editReadyMsg struct {
	selector string
	path     string
	err      error
}
type editorDoneMsg struct {
	selector string
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
	model := Model{ctx: ctx, app: app, launcher: launcher, input: input, spinner: spin, preview: vp, searchMode: searchModeName}
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
		if msg.String() == "ctrl+c" {
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
		if strings.TrimSpace(m.input.Value()) == msg.query && m.searchMode == searchModeFull {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.filtered = searchResultItems(m.items, msg.results)
				m.selected = 0
				m.keepSelectionVisible()
				commands = append(commands, m.loadPreview())
			}
		}
	case documentsMsg:
		if msg.sequence == m.listSequence {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.items = documentItems(msg.documents)
				m.selected = 0
				m.refreshFilter()
				commands = append(commands, m.loadPreview())
			}
		}
	case previewMsg:
		m.err = msg.err
		if msg.err == nil {
			m.preview.SetContent(msg.content)
			m.preview.GotoTop()
		}
	case scanMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
	case editReadyMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			command, err := m.launcher.EditorCommand(m.ctx, msg.path)
			if err != nil {
				m.err = err
				break
			}
			return m, tea.ExecProcess(command, func(err error) tea.Msg { return editorDoneMsg{selector: msg.selector, err: err} })
		}
	case editorDoneMsg:
		m.err = msg.err
		if msg.err == nil {
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
	switch msg.String() {
	case "esc":
		m.hideInput()
		m.refreshFilter()
		return m, m.loadPreview()
	case "tab":
		if m.searchMode == searchModeName {
			m.searchMode = searchModeFull
		} else {
			m.searchMode = searchModeName
		}
		if m.searchMode == searchModeFull && strings.TrimSpace(m.input.Value()) != "" {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, m.input.Value()))
		}
		m.refreshFilter()
		return m, m.loadPreview()
	case "enter":
		m.hideInput()
		if document, ok := m.selectedDocument(); ok {
			m.fullDocument = &document
			m.fullscreen = true
		}
		return m, m.loadPreview()
	case "up", "down", "pgup", "pgdown":
		return m.moveSelection(msg.String())
	case "ctrl+u":
		m.input.SetValue("")
		m.refreshFilter()
		return m, m.loadPreview()
	case "space", " ":
		if m.lastKeyAt.Add(doubleSpaceWindow).After(time.Now()) {
			m.spaceSequence++
			m.lastKeyAt = time.Time{}
			m.input.SetValue(strings.TrimSuffix(m.input.Value(), " "))
			m.hideInput()
			m.refreshFilter()
			return m, m.loadPreview()
		}
		m.lastKeyAt = time.Now()
		m.spaceSequence++
		sequence := m.spaceSequence
		return m, tea.Tick(doubleSpaceWindow, func(time.Time) tea.Msg { return spaceTimeoutMsg{sequence: sequence} })
	default:
		m.spaceSequence = 0
		m.lastKeyAt = time.Time{}
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		if m.searchMode == searchModeFull {
			query := strings.TrimSpace(m.input.Value())
			m.loading = true
			if query == "" {
				m.loading = false
				m.refreshFilter()
				return m, tea.Batch(command, m.loadPreview())
			}
			return m, tea.Batch(command, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, m.input.Value()))
		}
		m.refreshFilter()
		return m, tea.Batch(command, m.loadPreview())
	}
}

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "enter":
		if document, ok := m.selectedDocument(); ok {
			m.fullDocument = &document
			m.fullscreen = true
			return m, m.loadPreview()
		}
	case "space", " ":
		if m.lastKeyAt.Add(doubleSpaceWindow).After(time.Now()) {
			m.spaceSequence++
			m.lastKeyAt = time.Time{}
			commands = append(commands, m.toggleInput())
			return m, tea.Batch(commands...)
		}
		m.lastKeyAt = time.Now()
		m.spaceSequence++
		sequence := m.spaceSequence
		commands = append(commands, tea.Tick(doubleSpaceWindow, func(time.Time) tea.Msg { return spaceTimeoutMsg{sequence: sequence} }))
	case "up", "down", "pgup", "pgdown", "k", "j":
		if msg.String() == "k" {
			return m.moveSelection("up")
		}
		if msg.String() == "j" {
			return m.moveSelection("down")
		}
		return m.moveSelection(msg.String())
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
	m.keepSelectionVisible()
	return m, m.loadPreview()
}

func (m Model) visibleRows() int {
	if m.inputVisible {
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
		m.refreshFilter()
		return m.loadPreview()
	}
	m.inputVisible = true
	m.inputActive = true
	m.input.Focus()
	m.input.SetValue("")
	return textinput.Blink
}

func (m *Model) hideInput() {
	m.inputVisible = false
	m.inputActive = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *Model) refreshFilter() {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if !m.inputVisible || query == "" {
		m.filtered = append([]item(nil), m.items...)
	} else {
		m.filtered = m.filtered[:0]
		for _, candidate := range m.items {
			if wordsMatch(candidate.match, query) {
				m.filtered = append(m.filtered, candidate)
			}
		}
	}
	if len(m.filtered) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.filtered) {
		m.selected = len(m.filtered) - 1
	}
	m.keepSelectionVisible()
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
		return previewCmd(m.ctx, m.app, document.ID)
	}
	return func() tea.Msg { return previewMsg{content: previewPlaceholder("No document selected.")} }
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
	content := m.treePreviewView()
	lines := strings.Split(content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	parts := []string{strings.Join(lines, "\n")}
	if m.inputVisible {
		parts = append(parts, m.inputView())
	}
	parts = append(parts, m.statusBar())
	return strings.Join(parts, "\n")
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
	var lines []string
	for i := start; i < end; i++ {
		candidate := m.filtered[i]
		line := fitWidth(candidate.title, max(8, listWidth-2))
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

func (m Model) inputView() string {
	value := m.input.View()
	innerWidth := max(10, m.width-2)
	badge := m.modeBadge()
	fieldWidth := max(6, innerWidth-lipgloss.Width(badge)-1)
	field := lipgloss.NewStyle().Width(fieldWidth).MaxWidth(fieldWidth).Inline(true).Render(value)
	line := lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Inline(true).Render(badge + " " + field)
	border := lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderAccent)
	return border.Render(line)
}

func (m Model) statusBar() string {
	width := max(20, m.width)
	left := dimStyle.Render(m.hints())
	right := ""
	if m.loading {
		right += m.spinner.View() + " "
	}
	if m.err != nil {
		right += errorStyle.Render("Error: " + m.err.Error())
	} else {
		if m.inputVisible {
			right += accentStyle.Render("filter") + dimStyle.Render(fmt.Sprintf(" %d/%d", len(m.filtered), len(m.items)))
		} else {
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
	background := colors.Border
	if m.searchMode == searchModeFull {
		mode = " FULL "
		background = colors.Warning
	}
	return lipgloss.NewStyle().Background(background).Foreground(colors.Text).Bold(true).Inline(true).Render(mode)
}

func (m Model) hints() string {
	if m.inputVisible {
		return "tab mode • enter open • esc hide • type to search"
	}
	return "space×2 filter • enter open • ↑↓ select • r rescan • q quit"
}

func searchResultItems(items []item, results []membox.SearchResult) []item {
	allowed := make(map[string]bool, len(results))
	for _, result := range results {
		allowed[result.DocumentID] = true
	}
	filtered := make([]item, 0, len(results))
	for _, candidate := range items {
		if allowed[candidate.document.ID] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func documentItems(documents []membox.DocumentView) []item {
	items := make([]item, 0, len(documents))
	for _, document := range documents {
		title := displayTitle(document.Title, document.Path)
		items = append(items, item{document: document, title: title, match: document.Title + " " + document.Path + " " + document.Status + " " + document.ID})
	}
	return items
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
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

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
