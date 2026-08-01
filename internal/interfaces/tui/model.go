package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"membox"
	"membox/internal/interfaces/host"
)

const doubleSpaceWindow = 320 * time.Millisecond

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

type screen int

const (
	searchScreen screen = iota
	pathsScreen
	statusScreen
)

type itemKind int

const (
	documentItem itemKind = iota
	pathItem
	statusItem
)

type item struct {
	kind     itemKind
	document membox.DocumentView
	path     membox.PathView
	text     string
	match    string
}

type Model struct {
	ctx      context.Context
	app      App
	launcher host.Launcher

	input     textinput.Model
	pathInput textinput.Model
	preview   viewport.Model
	spinner   spinner.Model

	screen       screen
	items        []item
	filtered     []item
	selected     int
	filterActive bool
	allLoaded    bool
	addingPath   bool
	paths        []membox.PathView
	status       membox.IndexStatusView

	loading        bool
	err            error
	width, height  int
	searchSequence uint64
	listSequence   uint64
	spaceSequence  uint64
}

type searchMsg struct {
	sequence uint64
	results  []membox.SearchResult
	err      error
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
type pathsMsg struct {
	paths []membox.PathView
	err   error
}
type addPathMsg struct{ err error }
type statusMsg struct {
	status membox.IndexStatusView
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
	input.Placeholder = "type to search Markdown"
	input.Focus()
	pathInput := textinput.New()
	pathInput.Prompt = ""
	pathInput.Placeholder = "~/notes"
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	vp := viewport.New(40, 10)
	vp.SetContent("Type a query and press Enter.")
	model := Model{
		ctx: ctx, app: app, launcher: launcher,
		input: input, pathInput: pathInput, spinner: spin, preview: vp,
		screen: searchScreen, filtered: []item{},
	}
	model.preview.SetContent(previewPlaceholder("Search and press Enter."))
	return model
}

func Run(ctx context.Context, app App, launcher host.Launcher, programOptions ...tea.ProgramOption) error {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	options = append(options, programOptions...)
	_, err := tea.NewProgram(New(ctx, app, launcher), options...).Run()
	return err
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

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
		if m.pathInput.Focused() {
			return m.updatePathInput(msg)
		}
		if m.input.Focused() {
			return m.updateMainInput(msg)
		}
		return m.updateNavigation(msg)
	case searchMsg:
		if msg.sequence == m.searchSequence {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.items = searchItems(msg.results)
				m.selected = 0
				m.refreshFilter()
				commands = append(commands, m.loadPreview())
			}
		}
	case documentsMsg:
		if msg.sequence == m.listSequence {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.allLoaded = true
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
			m.preview.SetContent(fmt.Sprintf("Scan complete\n\nfiles %d\nadded %d\nupdated %d\nrenamed %d\nmissing %d", msg.report.Files, msg.report.Added, msg.report.Updated, msg.report.Renamed, msg.report.Missing))
		}
	case pathsMsg:
		m.loading, m.err, m.paths = false, msg.err, msg.paths
		m.items = pathItems(m.paths)
		m.selected = 0
		m.refreshFilter()
	case addPathMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			commands = append(commands, loadPathsCmd(m.ctx, m.app))
		}
	case statusMsg:
		m.loading, m.err, m.status = false, msg.err, msg.status
		m.items = statusItems(m.status)
		m.selected = 0
		m.refreshFilter()
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
		if msg.sequence == m.spaceSequence && m.input.Focused() && !m.filterActive {
			m.enterFilterMode()
			m.spaceSequence = 0
			if command := listDocumentsCmd(m.ctx, m.app, m.listSequence); !m.allLoaded {
				commands = append(commands, m.spinner.Tick, command)
			}
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

func (m Model) updatePathInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg.String() {
	case "enter":
		if strings.TrimSpace(m.pathInput.Value()) != "" {
			m.loading, m.addingPath = true, false
			m.pathInput.Blur()
			commands = append(commands, m.spinner.Tick, addPathCmd(m.ctx, m.app, m.pathInput.Value()))
		}
	case "esc":
		m.addingPath = false
		m.pathInput.Blur()
	default:
		var command tea.Cmd
		m.pathInput, command = m.pathInput.Update(msg)
		commands = append(commands, command)
	}
	return m, tea.Batch(commands...)
}

func (m Model) updateMainInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg.String() {
	case "esc":
		m.input.Blur()
		m.input.SetValue("")
		m.filterActive = false
		if m.screen == pathsScreen || m.screen == statusScreen {
			m.screen = searchScreen
		}
		m.refreshFilter()
		return m, m.loadPreview()
	case "enter":
		if m.filterActive {
			m.input.Blur()
			m.input.SetValue("")
			m.filterActive = false
			m.refreshFilter()
			return m, m.loadPreview()
		}
		m.searchSequence++
		m.loading, m.err, m.allLoaded = true, nil, false
		commands = append(commands, m.spinner.Tick, searchCmd(m.ctx, m.app, m.searchSequence, m.input.Value()))
	case "up", "down", "pgup", "pgdown":
		return m.moveSelection(msg.String())
	case "space":
		if m.filterActive {
			var command tea.Cmd
			m.input, command = m.input.Update(msg)
			m.refreshFilter()
			return m, tea.Batch(command, m.loadPreview())
		}
		m.spaceSequence++
		sequence := m.spaceSequence
		commands = append(commands, tea.Tick(doubleSpaceWindow, func(time.Time) tea.Msg { return spaceTimeoutMsg{sequence: sequence} }))
	default:
		m.spaceSequence = 0
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		if m.filterActive {
			m.refreshFilter()
			return m, tea.Batch(command, m.loadPreview())
		}
		commands = append(commands, command)
	}
	return m, tea.Batch(commands...)
}

func (m Model) updateNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "/", "tab":
		m.screen = searchScreen
		m.input.Focus()
		return m, textinput.Blink
	case "up", "down", "pgup", "pgdown", "k", "j":
		if msg.String() == "k" {
			return m.moveSelection("up")
		}
		if msg.String() == "j" {
			return m.moveSelection("down")
		}
		return m.moveSelection(msg.String())
	case "p":
		m.screen, m.loading = pathsScreen, true
		commands = append(commands, m.spinner.Tick, loadPathsCmd(m.ctx, m.app))
	case "s":
		m.screen, m.loading = statusScreen, true
		commands = append(commands, m.spinner.Tick, loadStatusCmd(m.ctx, m.app))
	case "a":
		if m.screen == pathsScreen {
			m.addingPath = true
			m.pathInput.SetValue("")
			m.pathInput.Focus()
			return m, textinput.Blink
		}
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
		m.selected = max(0, m.selected-m.preview.Height)
	case "pgdown":
		m.selected = min(max(0, len(m.filtered)-1), m.selected+m.preview.Height)
	}
	return m, m.loadPreview()
}

func (m *Model) enterFilterMode() {
	m.filterActive = true
	m.input.Focus()
	m.input.SetValue("")
	if !m.allLoaded {
		m.listSequence++
		m.loading = true
	}
}

func (m *Model) refreshFilter() {
	if m.screen != searchScreen {
		m.filtered = append([]item(nil), m.items...)
		return
	}
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if !m.filterActive || query == "" {
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
	if m.screen != searchScreen || len(m.filtered) == 0 || m.selected >= len(m.filtered) {
		return membox.DocumentView{}, false
	}
	candidate := m.filtered[m.selected]
	if candidate.kind != documentItem {
		return membox.DocumentView{}, false
	}
	return candidate.document, true
}

func (m Model) loadPreview() tea.Cmd {
	if m.screen == pathsScreen {
		return func() tea.Msg { return previewMsg{content: previewPlaceholder("Press a to add a path, r to scan.")} }
	}
	if m.screen == statusScreen {
		return func() tea.Msg { return previewMsg{content: statusPreview(m.status)} }
	}
	if document, ok := m.selectedDocument(); ok {
		return previewCmd(m.ctx, m.app, document.ID)
	}
	return func() tea.Msg { return previewMsg{content: previewPlaceholder("No document selected.")} }
}

func (m *Model) resize() {
	contentHeight := max(3, m.height-6)
	width := max(20, m.width-4)
	if m.width >= 100 {
		width = max(40, m.width*2/3-6)
	}
	m.preview.Width, m.preview.Height = width, contentHeight
	m.input.Width = max(10, m.width-5)
	m.pathInput.Width = max(10, m.width-5)
}

func (m Model) View() string {
	contentHeight := max(3, m.height-6)
	content := m.contentView()
	lines := strings.Split(content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n" + m.inputView() + "\n" + m.footerView()
}

func (m Model) contentView() string {
	switch m.screen {
	case pathsScreen:
		return m.pathsView()
	case statusScreen:
		return m.statusView()
	default:
		return m.searchView()
	}
}

func (m Model) searchView() string {
	var lines []string
	for i, candidate := range m.filtered {
		line := candidate.text
		if i == m.selected {
			line = lipgloss.NewStyle().Foreground(colors.Accent).Background(colors.SelectedBG).Render("> " + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(max(20, m.width/3)).Render(line))
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("No results.")}
	}
	list := strings.Join(lines, "\n")
	if m.width >= 100 {
		left := lipgloss.NewStyle().Width(max(30, m.width/3)).Render(list)
		right := lipgloss.NewStyle().Width(max(40, m.width*2/3-6)).Render(m.preview.View())
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return list + "\n\n" + m.preview.View()
}

func (m Model) pathsView() string {
	if len(m.filtered) == 0 {
		return dimStyle.Render("No configured paths.")
	}
	var lines []string
	for i, candidate := range m.filtered {
		prefix := "  "
		if i == m.selected {
			prefix = accentStyle.Render("> ")
		}
		lines = append(lines, prefix+candidate.text)
	}
	return strings.Join(lines, "\n")
}

func (m Model) statusView() string {
	var lines []string
	for _, candidate := range m.filtered {
		lines = append(lines, "  "+candidate.text)
	}
	return strings.Join(lines, "\n")
}

func (m Model) inputView() string {
	prompt := "❯ "
	label := "search"
	value := m.input.View()
	if m.filterActive {
		prompt = "rg "
		label = "filter"
	}
	if m.addingPath {
		prompt = "+  "
		label = "path"
		value = m.pathInput.View()
	}
	label = accentStyle.Render(label)
	border := "╭" + repeat("─", max(0, m.width-2)) + "╮"
	border = lipgloss.NewStyle().Foreground(colors.BorderMuted).Render(border)
	if m.input.Focused() || m.pathInput.Focused() {
		border = lipgloss.NewStyle().Foreground(colors.BorderAccent).Render(border)
	}
	return border + "\n" + prompt + value + " " + label
}

func (m Model) footerView() string {
	left := ""
	if m.loading {
		left += m.spinner.View() + " "
	}
	if m.err != nil {
		left += errorStyle.Render("Error: " + m.err.Error())
	} else {
		left += dimStyle.Render(fmt.Sprintf("%d shown / %d loaded", len(m.filtered), len(m.items)))
	}
	right := dimStyle.Render(m.hints())
	width := max(20, m.width)
	padding := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", padding) + right
}

func (m Model) hints() string {
	if m.addingPath {
		return "enter add • esc cancel"
	}
	if m.filterActive {
		return "enter select • esc clear • type to filter"
	}
	if m.screen == pathsScreen {
		return "a add • r scan • p paths • s status • q quit"
	}
	if m.screen == statusScreen {
		return "r scan • / search • p paths • q quit"
	}
	return "space×2 filter • enter search • ↑↓ select • e edit • o open • q quit"
}

func searchItems(results []membox.SearchResult) []item {
	items := make([]item, 0, len(results))
	for _, result := range results {
		document := membox.DocumentView{ID: result.DocumentID, Title: result.Title, Path: result.Path, Status: "active"}
		text := fmt.Sprintf("%s  %s", displayTitle(result.Title, result.Path), mutedStyle.Render(shortID(result.DocumentID)))
		items = append(items, item{kind: documentItem, document: document, text: text, match: result.Title + " " + result.Path + " " + result.Snippet + " " + result.DocumentID})
	}
	return items
}

func documentItems(documents []membox.DocumentView) []item {
	items := make([]item, 0, len(documents))
	for _, document := range documents {
		text := fmt.Sprintf("%s  %s  %s", displayTitle(document.Title, document.Path), mutedStyle.Render(document.Status), mutedStyle.Render(shortID(document.ID)))
		items = append(items, item{kind: documentItem, document: document, text: text, match: document.Title + " " + document.Path + " " + document.Status + " " + document.ID})
	}
	return items
}

func pathItems(paths []membox.PathView) []item {
	items := make([]item, 0, len(paths))
	for _, path := range paths {
		text := fmt.Sprintf("%d  %s  %s docs=%d", path.ID, path.Path, path.Status, path.Documents)
		items = append(items, item{kind: pathItem, path: path, text: text, match: fmt.Sprint(path.ID) + " " + path.Path + " " + path.Status})
	}
	return items
}

func statusItems(status membox.IndexStatusView) []item {
	texts := []string{
		fmt.Sprintf("paths     %d", status.Paths), fmt.Sprintf("active    %d", status.Active),
		fmt.Sprintf("missing   %d", status.Missing), fmt.Sprintf("untracked %d", status.Untracked),
		"database  " + status.DatabasePath,
	}
	items := make([]item, 0, len(texts))
	for _, text := range texts {
		items = append(items, item{kind: statusItem, text: text, match: text})
	}
	return items
}

func statusPreview(status membox.IndexStatusView) string {
	return fmt.Sprintf("Status\n\npaths: %d\nactive: %d\nmissing: %d\nuntracked: %d\ndatabase: %s", status.Paths, status.Active, status.Missing, status.Untracked, status.DatabasePath)
}

func displayTitle(title, path string) string {
	if title != "" {
		return title
	}
	if path == "" {
		return "(untitled)"
	}
	base := path[strings.LastIndex(path, "/")+1:]
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return base
}

func previewPlaceholder(message string) string {
	return headingStyle.Render("membox") + "\n\n" + dimStyle.Render(message)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
func repeat(value string, count int) string {
	if count <= 0 {
		return ""
	}
	return strings.Repeat(value, count)
}

func searchCmd(ctx context.Context, app App, sequence uint64, query string) tea.Cmd {
	return func() tea.Msg {
		results, err := app.SearchDocuments(ctx, membox.SearchDocumentsQuery{Query: query, Limit: 50})
		return searchMsg{sequence: sequence, results: results, err: err}
	}
}
func listDocumentsCmd(ctx context.Context, app App, sequence uint64) tea.Cmd {
	return func() tea.Msg {
		documents, err := app.ListDocuments(ctx, membox.ListDocumentsQuery{Limit: 500})
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
func addPathCmd(ctx context.Context, app App, directory string) tea.Cmd {
	return func() tea.Msg {
		_, err := app.AddPath(ctx, membox.AddPathCommand{Directory: directory})
		return addPathMsg{err: err}
	}
}
func loadPathsCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg { paths, err := app.ListPaths(ctx); return pathsMsg{paths: paths, err: err} }
}
func loadStatusCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg { status, err := app.GetIndexStatus(ctx); return statusMsg{status: status, err: err} }
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
