package tui

import (
	"context"
	"fmt"
	"os/exec"
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
	filename string
	match    string
}

type Model struct {
	ctx      context.Context
	app      App
	launcher host.Launcher

	input   textinput.Model
	preview viewport.Model
	spinner spinner.Model

	items         []item
	filtered      []item
	selected      int
	scrollTop     int
	rawContent    string
	rawDocumentID string

	inputVisible   bool
	inputActive    bool
	detailsVisible bool
	fullscreen     bool
	fullDocument   *membox.DocumentView

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
				commands = append(commands, m.loadPreview())
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
		if msg.sequence == m.spaceSequence && !m.inputActive {
			m.spaceSequence = 0
			m.lastKeyAt = time.Time{}
			m.detailsVisible = !m.detailsVisible
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
	case "ctrl+i":
		m.detailsVisible = !m.detailsVisible
		return m, nil
	case "enter":
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
		m.applyPreviewContent()
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
	if document, ok := m.selectedDocument(); ok && m.rawDocumentID == document.ID {
		m.applyPreviewContent()
		return m, nil
	}
	return m, m.loadPreview()
}

func (m Model) visibleRows() int {
	if m.inputVisible {
		if m.detailsVisible {
			return max(3, m.height-5)
		}
		return max(3, m.height-3)
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
		m.scrollTop = 0
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
	if m.inputVisible || m.searchMode == searchModeFull {
		query = strings.TrimSpace(m.input.Value())
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
	content := m.treePreviewView()
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
	timeLine := dimStyle.Render("modified ") + dateOnly(document.MTime)
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
			created, updated := dateOnly(candidate.document.MTime), dateOnly(candidate.document.MTime)
			dates = dimStyle.Render("  " + created + "  " + updated)
		} else if listWidth >= 52 {
			dates = dimStyle.Render("  " + dateOnly(candidate.document.MTime))
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
		right = m.spinner.View() + " " + right
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
		return "tab mode • ctrl+i details • enter open • esc hide • type to search"
	}
	return "space details • space×2 filter • enter open • ↑↓ select • r rescan • q quit"
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

func dateOnly(value int64) string {
	if value <= 0 {
		return "----------"
	}
	return time.Unix(0, value).Local().Format("2006-01-02")
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
