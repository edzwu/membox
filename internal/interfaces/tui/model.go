package tui

import (
	"context"
	"fmt"
	"os"
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
	DeleteDocument(context.Context, membox.DeleteDocumentCommand) (membox.DeleteDocumentResult, error)
	TrashSummary(context.Context) (membox.TrashSummaryResult, error)
	RenameDocument(context.Context, membox.RenameDocumentCommand) (membox.RenameDocumentResult, error)
	CreateNote(context.Context, membox.CreateNoteCommand) (membox.CreateNoteResult, error)
	CreateTopic(context.Context, membox.CreateTopicCommand) (membox.CreateTopicResult, error)
	ListTopics(context.Context, membox.ListTopicsQuery) ([]membox.TopicView, error)
	AddDocumentTopic(context.Context, membox.TopicMembershipCommand) (membox.TopicMembershipResult, error)
	RemoveDocumentTopic(context.Context, membox.TopicMembershipCommand) (membox.TopicMembershipResult, error)
	ListTopicDocuments(context.Context, membox.ListTopicDocumentsQuery) (membox.TopicDocumentsView, error)
	LinkDocuments(context.Context, membox.LinkDocumentsCommand) (membox.LinkDocumentsResult, error)
	UnlinkDocuments(context.Context, membox.UnlinkDocumentsCommand) (membox.UnlinkDocumentsResult, error)
	GetDocumentGraph(context.Context, membox.GetDocumentGraphQuery) (membox.DocumentGraphView, error)
	ToggleDocumentPin(context.Context, membox.ToggleDocumentPinCommand) (membox.ToggleDocumentPinResult, error)
	GetViewer(context.Context) (string, error)
	ListSettings(context.Context) ([]membox.SettingView, error)
	SetSetting(context.Context, string, string) error
	OpenDocumentWeb(context.Context, string) (string, error)
	WebStatus(context.Context) (membox.WebStatusView, error)
	EnsureWebCompanion(context.Context, string) (membox.WebStatusView, error)
	RestartWebCompanion(context.Context, string) (membox.WebStatusView, error)
	StopWeb(context.Context) error
	RenewWebLease(context.Context, string) error
	ReleaseWebLease(context.Context, string) error
	SetWebLifecycle(context.Context, string) error
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

const (
	inputModeSearch = "search"
	inputModeCmd    = "cmd"
	inputModeAgent  = "agent"
)

type commandSuggestion struct {
	Value       string
	Display     string
	Description string
	Action      func() tea.Msg
}

type commandResultMsg struct {
	text string
	err  error
}

type graphFocusMsg struct {
	documentID string
	cards      []membox.DocumentView
	incoming   int
	err        error
	previews   map[string]string
	layout     string
	topics     []membox.TopicView
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

	loading        bool
	err            error
	filterErr      error
	statusMessage  string
	width, height  int
	listSequence   uint64
	spaceSequence  uint64
	lastKeyAt      time.Time
	searchMode     string
	inputMode      string
	deleteConfirm  bool
	deleteSelector string
	deletePath     string
	commandHistory []string
	historyIndex   int
	cmdSuggestions []commandSuggestion
	cmdSelected    int
	cmdMenuVisible bool
	viewMode       string
	sortMode       string
	viewerMode     string
	hideNotes      bool
	configVisible  bool
	configSelected int
	settings       []membox.SettingView
	graphFocusID   string
	graphCards     []membox.DocumentView
	graphIncoming  int
	// Selection while walking the thread tree (index into graphCards).
	graphSelected int
	graphPreviews map[string]string
	// graphLayout is "thread" (the linear link list) or "star" (the one-hop
	// canvas graph: backlinks left, focus center, links right).
	graphLayout string
	graphTopics []membox.TopicView
	// Full-text filtering for the thread tree: name-mode filters match
	// filenames live; full-mode hits arrive async from SearchDocuments.
	graphSearchSequence uint64
	graphSearchQuery    string
	graphSearchHits     map[string]bool
	helpVisible         bool
	helpScroll          int

	// Web Companion control plane mirror plus the quit-time lifecycle prompt.
	web           webState
	webQuitPrompt bool

	// Pi Agent workspace (Companion HTTP/SSE client). Independent of filterErr.
	agent agentUIState
}

type searchMsg struct {
	query   string
	results []membox.SearchResult
	err     error
}
type threadSearchMsg struct {
	query    string
	sequence uint64
	results  []membox.SearchResult
	err      error
}

type documentsMsg struct {
	sequence  uint64
	documents []membox.DocumentView
	err       error
}
type viewerModeMsg struct {
	mode string
	err  error
}
type settingsMsg struct {
	settings []membox.SettingView
	err      error
}
type settingSavedMsg struct {
	key   string
	value string
	err   error
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
type pinMsg struct {
	documentID string
	pinned     bool
	err        error
}
type openMsg struct{ err error }
type openWebMsg struct {
	url string
	err error
}

type noteCreatedMsg struct {
	document membox.DocumentView
	command  *exec.Cmd
	err      error
}
type topicCreatedMsg struct{ topic membox.TopicView }
type deleteResultMsg struct {
	documentID string
	path       string
	trashCount int
	trashBytes int64
	err        error
}
type renamedMsg struct {
	documentID string
	path       string
	err        error
}
type spaceTimeoutMsg struct{ sequence uint64 }

// inputPlaceholder keeps the empty-input hint in sync with the active mode so
// the field never promises "filter documents" while a command or agent
// prompt is open.
func inputPlaceholder(mode string) string {
	switch mode {
	case inputModeCmd:
		return "command: note · topic · link · rename (tab for suggestions)"
	case inputModeAgent:
		return "ask the agent about your documents…"
	default:
		return "filter documents (tab toggles name/full search)"
	}
}

func New(ctx context.Context, app App, launcher host.Launcher) Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = inputPlaceholder(inputModeSearch)
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	vp := viewport.New(40, 10)
	model := Model{ctx: ctx, app: app, launcher: launcher, input: input, spinner: spin, preview: vp, searchMode: searchModeName, inputMode: inputModeSearch, viewMode: viewTree, sortMode: sortModeTime, viewerMode: "leaf", web: webState{controllerID: newWebControllerID()}, agent: newAgentUIState()}
	model.web.starting = true
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
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
		listDocumentsCmd(m.ctx, m.app, m.listSequence),
		viewerModeCmd(m.ctx, m.app),
		settingsCmd(m.ctx, m.app),
		webEnsureCmd(m.ctx, m.app, m.web.controllerID),
	)
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
	case tea.KeyMsg:
		if m.webQuitPrompt {
			return m.updateWebQuitPrompt(msg)
		}
		if msg.String() == "ctrl+d" {
			return m.beginQuit()
		}
		if m.helpVisible {
			return m.updateHelp(msg)
		}
		// "?" opens help. Ctrl+H toggles hidden selection notes; plain h stays
		// available for graph navigation.
		if msg.String() == "?" && !m.configVisible && !m.inputActive && m.graphFocusID == "" {
			m.helpVisible, m.helpScroll = true, 0
			return m, nil
		}
		if msg.String() == "ctrl+o" {
			m.configVisible = !m.configVisible
			if m.configVisible {
				m.keepSelectionVisible()
			}
			return m, nil
		}
		// Refresh works from any surface (tree, input, config) so a clipper
		// import can be picked up without hunting for the bare "r" binding.
		if msg.String() == "ctrl+r" {
			if m.configVisible {
				m.configVisible = false
			}
			return m, m.startScan()
		}
		if m.configVisible {
			return m.updateConfigPanel(msg)
		}
		if m.fullscreen {
			return m.updateFullscreen(msg)
		}
		if m.inputActive {
			return m.updateFilterInput(msg)
		}
		return m.updateNavigation(msg)
	case webStatusMsg:
		// Non-fatal when the companion is down: leaf viewer and CLI keep
		// working; the badge shows the degraded state permanently.
		if next := m.applyWebStatus(msg); next != nil {
			commands = append(commands, next)
		}
	case agentReadyMsg:
		return m.handleAgentReady(msg)
	case agentSessionMsg:
		return m.handleAgentSession(msg)
	case agentPromptMsg:
		return m.handleAgentPrompt(msg)
	case agentStreamReadyMsg:
		return m.handleAgentStreamReady(msg)
	case agentEventMsg:
		return m.handleAgentEvent(msg)
	case agentErrMsg:
		return m.handleAgentErr(msg)
	case webQuitResultMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("%s web companion before quit: %w", msg.action, msg.err)
			m.webQuitPrompt = msg.fromPrompt
		} else {
			if msg.notify {
				notifyWebKeptRunning(msg.url)
			}
			commands = append(commands, tea.Quit)
		}
	case settingsMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.settings = msg.settings
			for _, setting := range msg.settings {
				if setting.Key == "viewer" {
					m.viewerMode = setting.Value
				}
				if setting.Key == "hide_notes" {
					m.hideNotes = setting.Value == "on"
				}
			}
			m.refreshFilter()
		}
	case settingSavedMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			for index := range m.settings {
				if m.settings[index].Key == msg.key {
					m.settings[index].Value = msg.value
				}
			}
			if msg.key == "viewer" {
				m.viewerMode = msg.value
			}
			if msg.key == "hide_notes" {
				m.hideNotes = msg.value == "on"
				m.refreshFilter()
			}
			m.statusMessage = msg.key + ": " + msg.value
		}
	case viewerModeMsg:
		if msg.err != nil {
			m.loading, m.err = false, msg.err
		} else if msg.mode != "" {
			m.viewerMode = msg.mode
			if m.loading {
				m.loading = false
				m.statusMessage = "viewer: " + msg.mode
			}
			m.clearExecutedCommand()
		}
	case searchMsg:
		if m.fullTextFilterQuery() == msg.query {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.filtered = searchResultItems(m.items, msg.results, m.dateFilters, m.nameTextFilterQueries(), m.hideNotes)
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
				previousID := ""
				if current, ok := m.selectedDocument(); ok {
					previousID = current.ID
				}
				m.items = documentItems(msg.documents)
				m.refreshFilter()
				m.restoreSelection(previousID)
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
			m.listSequence++
			m.loading = true
			m.statusMessage = formatScanStatus(msg.report)
			commands = append(commands, m.spinner.Tick, listDocumentsCmd(m.ctx, m.app, m.listSequence))
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
		// Returning from the viewer keeps the thread tree on its original
		// focus — opening a linked document never re-roots the tree.
	case reindexMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			commands = append(commands, m.loadPreview())
		}
	case pinMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.applyPinnedState(msg.documentID, msg.pinned)
		}
	case openMsg:
		m.err = msg.err
	case openWebMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.statusMessage = "Opened in browser: " + msg.url
		}
	case noteCreatedMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.items = append(m.items, documentItems([]membox.DocumentView{msg.document})...)
			m.refreshFilter()
			for index, candidate := range m.filtered {
				if candidate.document.ID == msg.document.ID {
					m.selected = index
					break
				}
			}
			m.keepSelectionVisible()
			m.statusMessage = shortID(msg.document.ID) + " " + filepath.Base(msg.document.Path)
			m.clearExecutedCommand()
			if msg.command != nil {
				return m, tea.ExecProcess(msg.command, func(err error) tea.Msg {
					return editorDoneMsg{selector: msg.document.ID, viewer: true, err: err}
				})
			}
		}
		m.clearExecutedCommand()
	case topicCreatedMsg:
		m.loading = false
		m.statusMessage = "Topic ready: " + msg.topic.Name
		m.clearExecutedCommand()
	case deleteResultMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.deleteConfirm, m.deleteSelector, m.deletePath = false, "", ""
			m.statusMessage = "Moved " + filepath.Base(msg.path) + " to trash • restore: mm trash restore " + shortID(msg.documentID)
			if msg.trashBytes >= 256<<20 || msg.trashCount >= 200 {
				m.statusMessage += fmt.Sprintf(" • trash holds %d items (%s) — mm trash purge", msg.trashCount, formatBytesTUI(msg.trashBytes))
			}
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
		m.clearExecutedCommand()
	case graphFocusMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.graphFocusID = msg.documentID
			m.graphCards = msg.cards
			m.graphIncoming = msg.incoming
			m.graphPreviews = msg.previews
			m.graphLayout = msg.layout
			m.graphTopics = msg.topics
			m.graphSelected = 0
			m.viewMode = viewBoard
			m.boardScrollY = 0
			m.statusMessage = "graph focus " + shortID(msg.documentID)
			if m.graphLayout == "star" {
				// Star selection order is incoming…, focus, outgoing… — land on
				// the focus card.
				m.graphSelected = msg.incoming
				m.statusMessage += " · star (h/l switch sides, enter opens)"
			}
			m.graphSearchQuery, m.graphSearchHits = "", nil
			// An already-active full-text filter applies to the fresh thread too.
			if query := m.fullTextFilterQuery(); query != "" {
				commands = append(commands, m.threadSearchCmd(query))
			}
		}
		m.clearExecutedCommand()
	case threadSearchMsg:
		if msg.sequence == m.graphSearchSequence && msg.err == nil {
			m.graphSearchQuery = msg.query
			hits := make(map[string]bool, len(msg.results))
			for _, result := range msg.results {
				hits[result.DocumentID] = true
			}
			m.graphSearchHits = hits
			if m.graphFocusID != "" {
				if visible := len(m.visibleGraphIndices()); visible > 0 && m.graphSelected >= visible {
					m.graphSelected = visible - 1
				}
				m.scrollGraphToSelection()
			}
		}
	case commandResultMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.statusMessage = msg.text
		}
		m.clearExecutedCommand()
	case renamedMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.statusMessage = "Renamed " + filepath.Base(msg.path)
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
		m.clearExecutedCommand()
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
	if msg.String() == "ctrl+p" {
		return m, m.cycleInputMode()
	}
	if m.inputMode == inputModeCmd {
		return m.updateCommandInput(msg)
	}
	if m.inputMode == inputModeAgent {
		return m.updateAgentInput(msg)
	}
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
			if m.viewerMode == "web" {
				return m, tea.Batch(m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, document.ID))
			}
			return m, tea.Batch(m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
		}
		return m, nil
	case "up", "down", "pgup", "pgdown":
		return m.moveSelection(msg.String())
	case "home", "end":
		if m.viewMode == viewTree {
			return m.moveSelection(msg.String())
		}
		return m, nil
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
	m.statusMessage = ""
	var command tea.Cmd
	m.input, command = m.input.Update(msg)
	return m, m.filterChanged(command)
}

func (m Model) updateAgentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.hideInput()
		return m, nil
	case "enter":
		if strings.TrimSpace(m.input.Value()) == "" {
			return m, nil
		}
		// Attach current document as optional turn context.
		if doc, ok := m.selectedDocument(); ok {
			m.agent.documentID = doc.ID
			m.agent.documentTitle = displayTitle(doc.Title, doc.Path)
		} else {
			m.agent.documentID = ""
			m.agent.documentTitle = ""
		}
		return m.submitAgentPrompt()
	case "ctrl+c":
		if m.agent.runID != "" {
			return m.abortAgentRun()
		}
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	case "ctrl+u":
		m.input.SetValue("")
		return m, nil
	default:
		m.statusMessage = ""
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	}
}

func (m Model) updateCommandInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.cmdMenuVisible {
			m.cmdMenuVisible = false
			return m, nil
		}
		m.filterErr = nil
		m.hideInput()
		return m, nil
	case "up", "down":
		if m.cmdMenuVisible {
			if len(m.cmdSuggestions) == 0 {
				return m, nil
			}
			if msg.String() == "up" {
				m.cmdSelected = max(0, m.cmdSelected-1)
			} else {
				m.cmdSelected = min(len(m.cmdSuggestions)-1, m.cmdSelected+1)
			}
			return m, nil
		}
		if len(m.commandHistory) == 0 {
			return m, nil
		}
		if msg.String() == "up" {
			if m.historyIndex > 0 {
				m.historyIndex--
			}
		} else {
			if m.historyIndex < len(m.commandHistory) {
				m.historyIndex++
			}
		}
		if m.historyIndex == len(m.commandHistory) {
			m.input.SetValue("")
		} else {
			m.input.SetValue(m.commandHistory[m.historyIndex])
		}
		m.input.CursorEnd()
		return m, nil
	case "tab":
		m.cmdSuggestions = m.commandSuggestions()
		m.cmdSelected = 0
		m.cmdMenuVisible = len(m.cmdSuggestions) > 0
		if len(m.cmdSuggestions) == 0 {
			m.filterErr = fmt.Errorf("no command suggestions")
		} else {
			m.filterErr = nil
		}
		return m, nil
	case "enter":
		if m.cmdMenuVisible && len(m.cmdSuggestions) > 0 {
			return m.chooseCommandSuggestion(m.cmdSuggestions[m.cmdSelected])
		}
		return m.executeCommandInput()
	case "ctrl+u":
		m.input.SetValue("")
		m.historyIndex = len(m.commandHistory)
		m.cmdMenuVisible = false
		return m, nil
	default:
		m.cmdMenuVisible = false
		m.filterErr = nil
		m.statusMessage = ""
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	}
}

func (m Model) chooseCommandSuggestion(suggestion commandSuggestion) (tea.Model, tea.Cmd) {
	if suggestion.Action != nil {
		m.loading = true
		m.cmdMenuVisible = false
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg { return suggestion.Action() })
	}
	m.replaceLastCommandToken(suggestion.Value)
	m.cmdMenuVisible = false
	m.cmdSuggestions = nil
	m.filterErr = nil
	return m, nil
}

func (m *Model) replaceLastCommandToken(value string) {
	raw := m.input.Value()
	if strings.HasSuffix(raw, " ") || strings.HasSuffix(raw, "\t") {
		m.input.SetValue(raw + value)
	} else {
		trimmed := strings.TrimRight(raw, " \t")
		separator := strings.LastIndexAny(trimmed, " \t")
		if separator < 0 {
			m.input.SetValue(value)
		} else {
			m.input.SetValue(trimmed[:separator+1] + value)
		}
	}
	if !strings.HasSuffix(m.input.Value(), " ") {
		m.input.SetValue(m.input.Value() + " ")
	}
	m.input.CursorEnd()
}

func (m Model) executeCommandInput() (tea.Model, tea.Cmd) {
	tokens := commandTokens(m.input.Value())
	if len(tokens) == 0 {
		return m, nil
	}
	action, usage, err := m.commandAction(tokens)
	if err != nil {
		m.filterErr = fmt.Errorf("%v; usage: %s", err, usage)
		return m, nil
	}
	m.loading = true
	m.cmdMenuVisible = false
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg { return action() })
}

func commandTokens(value string) []string {
	return strings.Fields(strings.TrimSpace(value))
}

func (m Model) commandInputStructure() (tokens []string, index int, partial string) {
	raw := m.input.Value()
	tokens = commandTokens(raw)
	if strings.HasSuffix(raw, " ") || strings.HasSuffix(raw, "\t") {
		return tokens, len(tokens), ""
	}
	if len(tokens) == 0 {
		return nil, 0, ""
	}
	return tokens[:len(tokens)-1], len(tokens) - 1, tokens[len(tokens)-1]
}

func (m Model) commandSuggestions() []commandSuggestion {
	tokens, index, partial := m.commandInputStructure()
	if index == 0 {
		return filterCommandSuggestions([]commandSuggestion{
			{Value: "note", Display: "note", Description: "Create and manage notes"},
			{Value: "topic", Display: "topic", Description: "Manage topic documents"},
			{Value: "link", Display: "link", Description: "Manage document links"},
			{Value: "rename", Display: "rename", Description: "Rename selected file (keeps UUID)"},
			{Value: "web", Display: "web", Description: "Control the Web Companion"},
		}, partial)
	}
	if index == 1 {
		switch tokens[0] {
		case "note":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "new", Display: "new", Description: "Create a note from the selected document"},
				{Value: "view", Display: "view", Description: "Open a note with the configured viewer"},
			}, partial)
		case "topic":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "create", Display: "create", Description: "Create a topic document"},
				{Value: "list", Display: "list", Description: "List topics"},
				{Value: "add", Display: "add", Description: "Add document to topic"},
				{Value: "remove", Display: "remove", Description: "Remove document from topic"},
				{Value: "documents", Display: "documents", Description: "List documents in topic"},
			}, partial)
		case "link":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "add", Display: "add", Description: "Link two documents"},
				{Value: "remove", Display: "remove", Description: "Remove a document link"},
				{Value: "list", Display: "list", Description: "Show links and topics"},
			}, partial)
		case "web":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "status", Display: "status", Description: "Show Web Companion state"},
				{Value: "open", Display: "open", Description: "Open the reader in the browser"},
				{Value: "start", Display: "start", Description: "Start the Web Companion"},
				{Value: "stop", Display: "stop", Description: "Stop the Web Companion"},
				{Value: "keep", Display: "keep", Description: "Keep web running after TUI exit"},
			}, partial)
		}
		return nil
	}
	return m.commandArgumentSuggestions(tokens, index, partial)
}

func filterCommandSuggestions(suggestions []commandSuggestion, partial string) []commandSuggestion {
	if partial == "" {
		return suggestions
	}
	partial = strings.ToLower(partial)
	out := make([]commandSuggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		value := strings.ToLower(suggestion.Value)
		display := strings.ToLower(suggestion.Display)
		description := strings.ToLower(suggestion.Description)
		if strings.HasPrefix(value, partial) || strings.HasPrefix(display, partial) || strings.Contains(description, partial) {
			out = append(out, suggestion)
		}
	}
	return out
}

func (m Model) commandArgumentSuggestions(tokens []string, index int, partial string) []commandSuggestion {
	if len(tokens) < 2 {
		return nil
	}
	resource, verb := tokens[0], tokens[1]
	selected := ""
	if document, ok := m.selectedDocument(); ok {
		selected = document.ID
	}
	documentSuggestions := func() []commandSuggestion {
		var out []commandSuggestion
		if selected != "" {
			out = append(out, commandSuggestion{Value: "@selected", Display: "@selected", Description: "Current document: " + selected})
		}
		for _, candidate := range m.items {
			label := displayTitle(candidate.document.Title, candidate.document.Path)
			out = append(out, commandSuggestion{Value: candidate.document.ID, Display: shortID(candidate.document.ID), Description: candidate.filename + " " + label})
		}
		return filterCommandSuggestions(out, partial)
	}
	topicSuggestions := func() []commandSuggestion {
		var out []commandSuggestion
		for _, candidate := range m.items {
			base := filepath.Base(candidate.document.RelativePath)
			if !strings.HasPrefix(base, "topic-") || !strings.HasSuffix(strings.ToLower(base), ".md") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(base, "topic-"), filepath.Ext(base))
			out = append(out, commandSuggestion{Value: candidate.document.ID, Display: shortID(candidate.document.ID), Description: strings.ReplaceAll(name, "-", " ")})
		}
		return filterCommandSuggestions(out, partial)
	}
	switch resource {
	case "note":
		if verb == "view" && index == 2 {
			return documentSuggestions()
		}
	case "topic":
		switch verb {
		case "add", "remove":
			if index == 2 {
				return topicSuggestions()
			}
			if index == 3 {
				return documentSuggestions()
			}
		case "documents":
			if index == 2 {
				return topicSuggestions()
			}
		}
	case "link":
		if (verb == "add" || verb == "remove") && (index == 2 || index == 3) {
			return documentSuggestions()
		}
		if verb == "list" && index == 2 {
			return documentSuggestions()
		}
	}
	return nil
}

func (m Model) commandAction(tokens []string) (func() tea.Msg, string, error) {
	selected := ""
	if document, ok := m.selectedDocument(); ok {
		selected = document.ID
	}
	selector := func(value string) string {
		if value == "@selected" {
			return selected
		}
		return value
	}
	if len(tokens) < 2 {
		return nil, strings.Join(tokens, " "), fmt.Errorf("command verb is required")
	}
	switch tokens[0] {
	case "note":
		if tokens[1] == "new" && len(tokens) >= 3 {
			title := strings.Join(tokens[2:], " ")
			return func() tea.Msg {
				result, err := m.app.CreateNote(m.ctx, membox.CreateNoteCommand{Title: title, FromSelector: selected})
				if err != nil {
					return commandResultMsg{err: err}
				}
				editor, editorErr := m.launcher.EditorCommand(m.ctx, result.Document.Path)
				if editorErr != nil {
					return noteCreatedMsg{document: result.Document, err: editorErr}
				}
				return noteCreatedMsg{document: result.Document, command: editor}
			}, "note new <title>", nil
		}
		if tokens[1] == "view" && len(tokens) >= 3 {
			target := selector(tokens[2])
			webFlag := len(tokens) == 4 && tokens[3] == "--web"
			if len(tokens) > 3 && !webFlag {
				return nil, "note view <document-id> [--web]", fmt.Errorf("invalid note view arguments")
			}
			if webFlag {
				return func() tea.Msg {
					url, err := m.app.OpenDocumentWeb(m.ctx, target)
					if err != nil {
						return commandResultMsg{err: err}
					}
					command, openErr := m.launcher.OpenCommand(m.ctx, url)
					if openErr == nil {
						openErr = command.Run()
					}
					return openWebMsg{url: url, err: openErr}
				}, "note view <document-id> --web", nil
			}
			return func() tea.Msg {
				mode, err := m.app.GetViewer(m.ctx)
				if err != nil {
					return commandResultMsg{err: err}
				}
				if mode == "web" {
					url, err := m.app.OpenDocumentWeb(m.ctx, target)
					if err != nil {
						return commandResultMsg{err: err}
					}
					command, openErr := m.launcher.OpenCommand(m.ctx, url)
					if openErr == nil {
						openErr = command.Run()
					}
					return openWebMsg{url: url, err: openErr}
				}
				// Leaf viewer: same path as pressing Enter — editReadyMsg runs the
				// viewer through tea.ExecProcess (TUI suspends, leaf gets the real
				// terminal, TUI resumes after exit).
				return resolveViewerCmd(m.ctx, m.app, target)()
			}, "note view <document-id> [--web]", nil
		}
		return nil, "note new <title> | note view <document-id> [--web]", fmt.Errorf("invalid note command")
	case "topic":
		switch tokens[1] {
		case "create":
			if len(tokens) < 3 {
				return nil, "topic create <name>", fmt.Errorf("topic name is required")
			}
			name := strings.Join(tokens[2:], " ")
			return func() tea.Msg {
				result, err := m.app.CreateTopic(m.ctx, membox.CreateTopicCommand{Name: name})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return topicCreatedMsg{topic: result.Topic}
			}, "topic create <name>", nil
		case "list":
			if len(tokens) != 2 {
				return nil, "topic list", fmt.Errorf("topic list accepts no arguments")
			}
			return func() tea.Msg {
				topics, err := m.app.ListTopics(m.ctx, membox.ListTopicsQuery{})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return commandResultMsg{text: fmt.Sprintf("%d topic(s): %s", len(topics), topicNames(topics))}
			}, "topic list", nil
		case "add", "remove":
			if len(tokens) != 4 {
				return nil, "topic " + tokens[1] + " <topic-id> <document-id>", fmt.Errorf("topic and document are required")
			}
			command := membox.TopicMembershipCommand{DocumentSelector: selector(tokens[3]), TopicSelector: selector(tokens[2])}
			return func() tea.Msg {
				var result membox.TopicMembershipResult
				var err error
				if tokens[1] == "add" {
					result, err = m.app.AddDocumentTopic(m.ctx, command)
				} else {
					result, err = m.app.RemoveDocumentTopic(m.ctx, command)
				}
				if err != nil {
					return commandResultMsg{err: err}
				}
				verb := "added to"
				if tokens[1] == "remove" {
					verb = "removed from"
				}
				return commandResultMsg{text: fmt.Sprintf("Document %s topic %q", verb, result.Topic.Name)}
			}, "topic " + tokens[1] + " <topic-id> <document-id>", nil
		case "documents":
			if len(tokens) != 3 {
				return nil, "topic documents <topic-id>", fmt.Errorf("topic is required")
			}
			return func() tea.Msg {
				result, err := m.app.ListTopicDocuments(m.ctx, membox.ListTopicDocumentsQuery{Selector: selector(tokens[2])})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return commandResultMsg{text: fmt.Sprintf("topic %q has %d document(s)", result.Topic.Name, len(result.Documents))}
			}, "topic documents <topic-id>", nil
		}
	case "link":
		switch tokens[1] {
		case "add", "remove":
			if len(tokens) != 4 {
				return nil, "link " + tokens[1] + " <from-id> <to-id>", fmt.Errorf("from and to documents are required")
			}
			fromSelector, toSelector := selector(tokens[2]), selector(tokens[3])
			return func() tea.Msg {
				var err error
				if tokens[1] == "add" {
					_, err = m.app.LinkDocuments(m.ctx, membox.LinkDocumentsCommand{FromSelector: fromSelector, ToSelector: toSelector})
				} else {
					_, err = m.app.UnlinkDocuments(m.ctx, membox.UnlinkDocumentsCommand{FromSelector: fromSelector, ToSelector: toSelector})
				}
				if err != nil {
					return commandResultMsg{err: err}
				}
				verb := "linked"
				if tokens[1] == "remove" {
					verb = "unlinked"
				}
				return commandResultMsg{text: fmt.Sprintf("Documents %s", verb)}
			}, "link " + tokens[1] + " <from-id> <to-id>", nil
		case "list":
			if len(tokens) != 3 {
				return nil, "link list <document-id>", fmt.Errorf("document is required")
			}
			return graphFocusCmd(m.ctx, m.app, selector(tokens[2]), "thread"), "link list <document-id>", nil
		case "graph":
			if len(tokens) != 3 {
				return nil, "link graph <document-id>", fmt.Errorf("document is required")
			}
			return graphFocusCmd(m.ctx, m.app, selector(tokens[2]), "star"), "link graph <document-id>", nil
		}
	case "rename":
		// Rename operates on the highlighted document so the command stays a
		// single argument: `rename new-name.md`.
		if len(tokens) < 2 {
			return nil, "rename <new-filename>", fmt.Errorf("new filename is required")
		}
		if selected == "" {
			return nil, "rename <new-filename>", fmt.Errorf("no document is selected")
		}
		name := strings.Join(tokens[1:], " ")
		return func() tea.Msg {
			result, err := m.app.RenameDocument(m.ctx, membox.RenameDocumentCommand{Selector: selected, NewFilename: name})
			if err != nil {
				return renamedMsg{err: err}
			}
			return renamedMsg{documentID: result.DocumentID, path: result.Path}
		}, "rename <new-filename>", nil
	case "web":
		return m.webCommandAction(tokens)
	}
	return nil, strings.Join(tokens, " "), fmt.Errorf("unknown command %q", tokens[0])
}

func topicNames(topics []membox.TopicView) string {
	names := make([]string, 0, len(topics))
	for _, topic := range topics {
		names = append(names, topic.Name)
	}
	return strings.Join(names, ", ")
}

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
	switch msg.String() {
	case "q":
		// Contextual "back" within the TUI; Ctrl+D quits the program.
		if m.graphFocusID != "" {
			m.graphFocusID = ""
			m.graphCards = nil
			m.graphIncoming = 0
			m.graphSelected = 0
			m.graphSearchQuery, m.graphSearchHits = "", nil
			m.graphLayout, m.graphTopics = "", nil
			m.viewMode = viewTree
			m.keepSelectionVisible()
		} else if m.viewMode == viewBoard {
			m.viewMode = viewTree
			m.keepSelectionVisible()
		} else if m.detailsVisible {
			m.detailsVisible = false
		}
		m.statusMessage = ""
		return m, nil
	case "enter":
		if document, ok := m.selectedDocument(); ok {
			if m.viewerMode == "web" {
				m.loading = true
				commands = append(commands, m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, document.ID))
			} else {
				m.loading = true
				commands = append(commands, m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
			}
		}
	case "space", " ":
		if m.lastKeyAt.Add(doubleSpaceWindow).After(time.Now()) {
			// double space confirmed: toggle input
			m.spaceSequence++
			m.lastKeyAt = time.Time{}
			commands = append(commands, m.openInput(m.inputMode))
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
		// Toggle between tree and board view when input is not active
		m.graphFocusID = ""
		m.graphCards = nil
		m.graphSelected = 0
		m.graphIncoming = 0
		m.graphSearchQuery, m.graphSearchHits = "", nil
		m.graphLayout, m.graphTopics = "", nil
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
		return m, m.cycleInputMode()
	case "s":
		m.toggleSort()
		return m, m.loadPreview()
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
		return m, m.startScan()
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

func (m *Model) cycleInputMode() tea.Cmd {
	switch m.inputMode {
	case inputModeSearch:
		m.inputMode = inputModeCmd
	case inputModeCmd:
		m.inputMode = inputModeAgent
	case inputModeAgent:
		m.inputMode = inputModeSearch
	}
	m.input.Placeholder = inputPlaceholder(m.inputMode)
	m.cmdSuggestions = nil
	m.cmdSelected = 0
	m.cmdMenuVisible = false
	m.filterErr = nil
	return m.filterChanged(nil)
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
		commands := []tea.Cmd{inputCommand, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, query)}
		// The visible thread filters by the same full-text query: body hits
		// arrive via threadSearchMsg and intersect the linked cards.
		if m.graphFocusID != "" {
			commands = append(commands, m.threadSearchCmd(query))
		}
		return tea.Batch(commands...)
	}
	m.graphSearchQuery, m.graphSearchHits = "", nil
	m.loading = false
	m.refreshFilter()
	return tea.Batch(inputCommand, m.loadPreview())
}

func (m *Model) refreshFilter() {
	nameQueries := m.nameTextFilterQueries()
	m.filtered = m.filtered[:0]
	for _, candidate := range m.items {
		if m.hideNotes && isClippedNote(candidate.filename) {
			continue
		}
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
	// A live thread view shares the same filters: keep its selection inside
	// the newly visible cards.
	if m.graphFocusID != "" {
		if visible := len(m.visibleGraphIndices()); visible > 0 && m.graphSelected >= visible {
			m.graphSelected = visible - 1
		}
		m.scrollGraphToSelection()
	}
}

// startScan runs path scan then reloads the document tree (ctrl+r / r).
func (m *Model) startScan() tea.Cmd {
	m.loading = true
	m.err = nil
	m.statusMessage = "scanning…"
	m.deleteConfirm = false
	return tea.Batch(m.spinner.Tick, scanCmd(m.ctx, m.app))
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

func (m *Model) toggleSort() {
	if m.sortMode == sortModeTime {
		m.sortMode = sortModeName
	} else {
		m.sortMode = sortModeTime
	}
	m.sortFiltered()
	m.selected = 0
	m.scrollTop = 0
	m.boardScrollY = 0
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
		if left.document.Pinned != right.document.Pinned {
			return left.document.Pinned
		}
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

// updateConfigPanel handles keys while the settings panel is open: ↑↓ selects
// a row, ←→ cycles the value (saving immediately), esc/enter closes.
func (m Model) updateConfigPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q":
		m.configVisible = false
		return m, nil
	case "up", "k":
		if m.configSelected > 0 {
			m.configSelected--
		}
		return m, nil
	case "down", "j":
		if m.configSelected+1 < len(m.settings) {
			m.configSelected++
		}
		return m, nil
	case "left", "h":
		return m, m.cycleSetting(-1)
	case "right", "l", "space", " ":
		return m, m.cycleSetting(1)
	case "o":
		if !m.web.running() {
			return m, nil
		}
		return m, tea.ExecProcess(openCommand(m.ctx, m.launcher, m.web.status.URL), func(err error) tea.Msg { return openWebMsg{url: m.web.status.URL, err: err} })
	case "x":
		if m.web.running() {
			return m, tea.Batch(m.spinner.Tick, webStopCmd(m.ctx, m.app))
		}
		m.web.starting = true
		return m, tea.Batch(m.spinner.Tick, webEnsureCmd(m.ctx, m.app, m.web.controllerID))
	}
	return m, nil
}

// openCommand builds the platform browser opener, ignoring errors until the
// returned command runs (tea.ExecProcess surfaces them there).
func openCommand(ctx context.Context, launcher host.Launcher, url string) *exec.Cmd {
	command, err := launcher.OpenCommand(ctx, url)
	if err != nil {
		return exec.Command("false")
	}
	return command
}

func (m Model) cycleSetting(direction int) tea.Cmd {
	if len(m.settings) == 0 || m.configSelected >= len(m.settings) {
		return nil
	}
	setting := m.settings[m.configSelected]
	if len(setting.Options) == 0 {
		return nil
	}
	index := 0
	for i, option := range setting.Options {
		if option == setting.Value {
			index = i
			break
		}
	}
	next := (index + direction + len(setting.Options)) % len(setting.Options)
	value := setting.Options[next]
	if value == setting.Value {
		return nil
	}
	m.loading = true
	return tea.Batch(m.spinner.Tick, setSettingCmd(m.ctx, m.app, setting.Key, value))
}

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
	filenameWidth := max(12, listWidth-uuidWidth-8)
	if listWidth >= 72 {
		filenameWidth = max(16, listWidth-uuidWidth-30)
	} else if listWidth >= 52 {
		filenameWidth = max(14, listWidth-uuidWidth-20)
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
		line := lipgloss.NewStyle().Width(listWidth - 2).MaxWidth(listWidth - 2).Inline(true).Render(pin + uuid + "  " + filename + dates)
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
	nameQueries := m.nameTextFilterQueries()
	fullQuery := m.fullTextFilterQuery()
	// Only apply body hits once they belong to the current query; before the
	// async result arrives the thread keeps showing its cards.
	fullReady := fullQuery != "" && m.graphSearchQuery == fullQuery && m.graphSearchHits != nil
	if len(nameQueries) == 0 && len(m.dateFilters) == 0 && !fullReady {
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
		if matchesTextFilters(candidate.match, nameQueries) && matchesDateFilters(doc, m.dateFilters) {
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
func (m Model) openGraphSelection() (tea.Model, tea.Cmd) {
	var target membox.DocumentView
	if m.graphLayout == "star" {
		visIn, visOut := m.visibleStarSegments()
		p := m.graphSelected
		switch {
		case p >= 0 && p < len(visIn):
			target = m.graphCards[visIn[p]]
		case p == len(visIn):
			target = m.graphCards[0]
		case p > len(visIn) && p-len(visIn)-1 < len(visOut):
			target = m.graphCards[visOut[p-len(visIn)-1]]
		default:
			return m, nil
		}
	} else {
		visible := m.visibleGraphIndices()
		if m.graphSelected < 0 || m.graphSelected >= len(visible) {
			return m, nil
		}
		target = m.graphCards[visible[m.graphSelected]]
	}
	m.loading = true
	if m.viewerMode == "web" {
		return m, tea.Batch(m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, target.ID))
	}
	return m, tea.Batch(m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, target.ID))
}

// moveStarSelection walks the star canvas: up/down stay inside a column,
// left/right hop between backlinks, focus, and links.
func (m *Model) moveStarSelection(key string) {
	visIn, visOut := m.visibleStarSegments()
	inN, outN := len(visIn), len(visOut)
	total := inN + 1 + outN
	if total == 0 {
		return
	}
	if m.graphSelected < 0 {
		m.graphSelected = 0
	}
	if m.graphSelected > total-1 {
		m.graphSelected = total - 1
	}
	var segs [][2]int
	if inN > 0 {
		segs = append(segs, [2]int{0, inN - 1})
	}
	segs = append(segs, [2]int{inN, inN})
	if outN > 0 {
		segs = append(segs, [2]int{inN + 1, total - 1})
	}
	current := 0
	for i, seg := range segs {
		if m.graphSelected >= seg[0] && m.graphSelected <= seg[1] {
			current = i
		}
	}
	pos := m.graphSelected - segs[current][0]
	size := segs[current][1] - segs[current][0]
	switch key {
	case "up", "k":
		if pos > 0 {
			pos--
		}
	case "down", "j":
		if pos < size {
			pos++
		}
	case "left", "h":
		if current > 0 {
			current--
			pos = min(pos, segs[current][1]-segs[current][0])
		}
	case "right", "l":
		if current < len(segs)-1 {
			current++
			pos = min(pos, segs[current][1]-segs[current][0])
		}
	case "pgup":
		pos = max(0, pos-5)
	case "pgdown":
		pos = min(size, pos+5)
	case "home":
		current, pos = 0, 0
	case "end":
		current, pos = len(segs)-1, segs[len(segs)-1][1]-segs[len(segs)-1][0]
	}
	m.graphSelected = segs[current][0] + pos
	m.scrollGraphToSelection()
}

// moveGraphSelection walks the thread-tree selection and keeps it in view.
func (m *Model) moveGraphSelection(key string) {
	if m.graphLayout == "star" {
		m.moveStarSelection(key)
		return
	}
	n := len(m.visibleGraphIndices())
	if n == 0 {
		return
	}
	if m.graphSelected < 0 {
		m.graphSelected = 0
	}
	if m.graphSelected > n-1 {
		m.graphSelected = n - 1
	}
	switch key {
	case "up", "left", "k":
		if m.graphSelected > 0 {
			m.graphSelected--
		}
	case "down", "right", "j", "l":
		if m.graphSelected < n-1 {
			m.graphSelected++
		}
	case "pgup":
		m.graphSelected = max(0, m.graphSelected-5)
	case "pgdown":
		m.graphSelected = min(n-1, m.graphSelected+5)
	case "home":
		m.graphSelected = 0
	case "end":
		m.graphSelected = n - 1
	}
	m.scrollGraphToSelection()
}

// scrollGraphToSelection adjusts boardScrollY so the selected thread card is
// visible.
func (m *Model) scrollGraphToSelection() {
	var rows []string
	var starts []int
	if m.graphLayout == "star" {
		rows, starts = m.graphStarRows()
	}
	if len(rows) == 0 {
		rows, starts = m.graphRows()
	}
	if len(starts) == 0 {
		m.boardScrollY = 0
		return
	}
	if m.graphSelected < 0 || m.graphSelected >= len(starts) {
		return
	}
	cardTop := starts[m.graphSelected]
	cardHeight := len(rows) - cardTop
	if m.graphSelected+1 < len(starts) {
		cardHeight = starts[m.graphSelected+1] - cardTop
	}
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
	if maxScroll := len(rows) - visible; maxScroll >= 0 && m.boardScrollY > maxScroll {
		m.boardScrollY = maxScroll
	}
	if m.boardScrollY < 0 {
		m.boardScrollY = 0
	}
}

// threadSearchCmd runs the full-text query against the index so the thread
// tree can filter cards by body content — the same FTS path the document list
// uses. Name-mode filters never need it.
func (m *Model) threadSearchCmd(query string) tea.Cmd {
	m.graphSearchSequence++
	sequence := m.graphSearchSequence
	return func() tea.Msg {
		results, err := m.app.SearchDocuments(m.ctx, membox.SearchDocumentsQuery{Query: query, Limit: 100})
		return threadSearchMsg{query: query, sequence: sequence, results: results, err: err}
	}
}

// graphFocusCmd loads a document's link graph (with body previews) so it can
// be walked as a thread tree or drawn as a one-hop star canvas. layout is
// "thread" or "star".
func graphFocusCmd(ctx context.Context, app App, selectorValue string, layout string) tea.Cmd {
	return func() tea.Msg {
		graph, err := app.GetDocumentGraph(ctx, membox.GetDocumentGraphQuery{Selector: selectorValue})
		if err != nil {
			return graphFocusMsg{err: err}
		}
		cards := make([]membox.DocumentView, 0, 1+len(graph.Outgoing)+len(graph.Incoming))
		cards = append(cards, graph.Focus)
		cards = append(cards, graph.Outgoing...)
		cards = append(cards, graph.Incoming...)
		// Load body previews so cards show content, not just filenames.
		previews := make(map[string]string, len(cards))
		for _, card := range cards {
			if body, readErr := app.ReadDocument(ctx, membox.ReadDocumentQuery{Selector: card.ID}); readErr == nil {
				if preview := host.DocumentPreview(body, 220); preview != "" {
					previews[card.ID] = preview
				}
			}
		}
		return graphFocusMsg{documentID: graph.Focus.ID, cards: cards, incoming: len(graph.Incoming), previews: previews, layout: layout, topics: graph.Topics}
	}
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
	left := dimStyle.Render(m.hints()) + "  " + m.webBadge()
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
					mode = "full"
				}
			}
			right += accentStyle.Render(mode) + dimStyle.Render(fmt.Sprintf(" %d/%d", len(m.filtered), len(m.items)))
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
		switch m.agent.badge {
		case "READY":
			mode = " AGENT READY "
		case "RUNNING":
			mode = " AGENT RUNNING "
		case "CONNECTING":
			mode = " AGENT CONNECTING "
		case "ERROR":
			mode = " AGENT ERROR "
		default:
			mode = " AGENT OFF "
		}
		background = lipgloss.AdaptiveColor{Dark: "#7048a8", Light: "#eadcff"}
		foreground = lipgloss.AdaptiveColor{Dark: "#ffffff", Light: "#3c1f63"}
	default:
		if m.searchMode == searchModeFull {
			mode = " FULL "
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

func searchResultItems(items []item, results []membox.SearchResult, dateFilters []dateFilter, nameQueries []string, hideNotes bool) []item {
	allowed := make(map[string]bool, len(results))
	for _, result := range results {
		allowed[result.DocumentID] = true
	}
	filtered := make([]item, 0, len(results))
	for _, candidate := range items {
		if hideNotes && isClippedNote(candidate.filename) {
			continue
		}
		if allowed[candidate.document.ID] && matchesDateFilters(candidate.document, dateFilters) && matchesTextFilters(candidate.match, nameQueries) {
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
func togglePinCmd(ctx context.Context, app App, selector string) tea.Cmd {
	return func() tea.Msg {
		result, err := app.ToggleDocumentPin(ctx, membox.ToggleDocumentPinCommand{Selector: selector})
		return pinMsg{documentID: result.DocumentID, pinned: result.Pinned, err: err}
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
