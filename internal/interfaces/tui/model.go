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

	"membox"
	"membox/internal/interfaces/host"
)

const doubleSpaceWindow = 320 * time.Millisecond

const (
	searchModeName = "name"
	searchModeFull = "full"
)

const (
	mediaScopeAll      = "all"
	mediaScopeMarkdown = "markdown"
	mediaScopePDF      = "pdf"
	mediaScopeImage    = "image"
)

const (
	viewTree  = "tree"
	viewBoard = "board"
)

type App interface {
	AddPath(context.Context, membox.AddPathCommand) (membox.AddPathResult, error)
	SearchDocuments(context.Context, membox.SearchDocumentsQuery) ([]membox.SearchResult, error)
	ListDocuments(context.Context, membox.ListDocumentsQuery) ([]membox.DocumentView, error)
	SetDocumentSummary(context.Context, membox.SetSummaryCommand) (membox.DocumentView, error)
	SummarizeDocument(context.Context, string) (membox.DocumentView, error)
	ReadDocument(context.Context, membox.ReadDocumentQuery) ([]byte, error)
	ResolveDocumentLocation(context.Context, membox.ResolveLocationQuery) (membox.LocationView, error)
	ReindexDocument(context.Context, membox.ReindexDocumentCommand) error
	DeleteDocument(context.Context, membox.DeleteDocumentCommand) (membox.DeleteDocumentResult, error)
	TrashSummary(context.Context) (membox.TrashSummaryResult, error)
	RenameDocument(context.Context, membox.RenameDocumentCommand) (membox.RenameDocumentResult, error)
	UpdatePDFMetadata(context.Context, membox.UpdatePDFMetadataCommand) (membox.DocumentView, error)
	ConvertPDF(context.Context, membox.ConvertPDFCommand) (membox.ConvertPDFResult, error)
	SetPDFConverterServer(context.Context, string) (membox.PDFConverterConfigView, error)
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
	document     membox.DocumentView
	title        string
	filename     string
	match        string
	pdfConverted bool
}

type textFilter struct {
	Value    string
	Mode     string
	Sequence uint64
	Exact    bool // 全匹配: true = 整词匹配（词边界，非子串）
	Case     bool // 区分大小写
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

	items           []item
	filtered        []item
	dateFilters     []dateFilter
	textFilters     []textFilter
	filterSequence  uint64
	selected        int
	scrollTop       int
	boardScrollY    int
	rawContent      string
	rawDocumentID   string
	renderedPreview string // styled preview for rawContent at renderedWidth
	renderedWidth   int
	previewGen      uint64 // drops stale async preview renders

	inputVisible   bool
	inputActive    bool
	detailsVisible bool
	previewFocused bool
	fullscreen     bool
	fullDocument   *membox.DocumentView

	loading       bool
	err           error
	filterErr     error
	statusMessage string
	summarizing   map[string]bool

	// PDF conversion is one synchronous remote call. The streaming endpoint
	// reports truthful chunk/page boundaries; upload and cache-hit phases keep
	// the indeterminate animation instead of fabricating continuous progress.
	pdfConversionActive    bool
	pdfConversionID        string
	pdfConversionStarted   time.Time
	pdfProgressFrame       int
	pdfProgressSequence    uint64
	pdfProgressStage       string
	pdfProgressDescription string
	pdfProgressCompleted   int
	pdfProgressTotal       int
	width, height          int
	listSequence           uint64
	scanning               bool
	scanRefreshSequence    uint64
	scanKnownDocuments     map[string]bool
	scanNewDocuments       int
	scanFocusedDocumentID  string
	scanStatusBase         string
	spaceSequence          uint64
	lastKeyAt              time.Time
	searchMode             string
	inputMode              string
	mediaScope             string
	deleteConfirm          bool
	deleteSelector         string
	deletePath             string
	commandHistory         []string
	historyIndex           int
	cmdSuggestions         []commandSuggestion
	cmdSelected            int
	cmdMenuVisible         bool
	viewMode               string
	viewerMode             string
	hideNotes              bool
	configVisible          bool
	configSelected         int
	settings               []membox.SettingView
	// Filter-options panel (ctrl+o while the input is focused): match
	// semantics (contains ⇄ exact) and case sensitivity are orthogonal to the
	// name ⇄ content scope, so they live on their own panel and show up on the
	// status bar, not inside the mode badge.
	filterExact           bool // 全匹配: true = title/filename 精确全等
	filterCase            bool // 区分大小写
	filterOptionsVisible  bool
	filterOptionsSelected int
	graphFocusID          string
	graphCards            []membox.DocumentView
	graphIncoming         int
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
	exact   bool
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
	content    string // raw Markdown (or plain placeholder)
	rendered   string // pre-rendered ANSI at width (may be empty on error path)
	width      int
	generation uint64
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
type summarizeDoneMsg struct {
	documentID string
	document   membox.DocumentView
	err        error
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
	title      string
	virtual    bool
	err        error
}
type pdfConvertedMsg struct {
	result membox.ConvertPDFResult
	err    error
}
type pdfConversionRun struct{ events chan tea.Msg }
type pdfConversionProgressMsg struct {
	run      *pdfConversionRun
	sequence uint64
	progress membox.PDFConversionProgress
}
type pdfProgressTickMsg struct{ sequence uint64 }
type spaceTimeoutMsg struct{ sequence uint64 }

func startPDFConversionCmd(ctx context.Context, app App, selector string, sequence uint64) tea.Cmd {
	run := &pdfConversionRun{events: make(chan tea.Msg, 32)}
	return func() tea.Msg {
		go func() {
			result, err := app.ConvertPDF(ctx, membox.ConvertPDFCommand{
				Selector: selector,
				OnProgress: func(progress membox.PDFConversionProgress) {
					select {
					case run.events <- pdfConversionProgressMsg{run: run, sequence: sequence, progress: progress}:
					case <-ctx.Done():
					}
				},
			})
			select {
			case run.events <- pdfConvertedMsg{result: result, err: err}:
			case <-ctx.Done():
			}
			close(run.events)
		}()
		message, _ := <-run.events
		return message
	}
}

func waitPDFConversionCmd(run *pdfConversionRun) tea.Cmd {
	return func() tea.Msg {
		message, _ := <-run.events
		return message
	}
}

func pdfProgressTickCmd(sequence uint64) tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return pdfProgressTickMsg{sequence: sequence}
	})
}

func (m *Model) beginPDFProgress(documentID string) tea.Cmd {
	m.loading = true
	m.err = nil
	m.statusMessage = ""
	m.pdfConversionActive = true
	m.pdfConversionID = documentID
	m.pdfConversionStarted = time.Now()
	m.pdfProgressFrame = 0
	m.pdfProgressStage = ""
	m.pdfProgressDescription = "uploading PDF"
	m.pdfProgressCompleted = 0
	m.pdfProgressTotal = 0
	m.pdfProgressSequence++
	return pdfProgressTickCmd(m.pdfProgressSequence)
}

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
		return "filter documents (enter opens · tab pins · ctrl+f name/content)"
	}
}

func New(ctx context.Context, app App, launcher host.Launcher) Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = inputPlaceholder(inputModeSearch)
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	vp := viewport.New(40, 10)
	model := Model{ctx: ctx, app: app, launcher: launcher, input: input, spinner: spin, preview: vp, searchMode: searchModeName, inputMode: inputModeSearch, mediaScope: mediaScopeAll, viewMode: viewTree, viewerMode: "leaf", summarizing: map[string]bool{}, web: webState{controllerID: newWebControllerID()}, agent: newAgentUIState()}
	model.web.starting = true
	model.preview.SetContent(previewPlaceholder("Loading documents…"))
	return model
}

func Run(ctx context.Context, app App, launcher host.Launcher, programOptions ...tea.ProgramOption) error {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	options = append(options, programOptions...)
	model := New(ctx, app, launcher)
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	go maintainWebLease(leaseCtx, app, model.web.controllerID, webRefreshInterval)
	_, err := tea.NewProgram(model, options...).Run()
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
		if cmd := m.resize(); cmd != nil {
			commands = append(commands, cmd)
		}
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
			// While typing a filter, ctrl+o adjusts the filter options
			// (match semantics + case); elsewhere it is the web/settings panel.
			if m.inputActive {
				return m.updateFilterInput(msg)
			}
			m.configVisible = !m.configVisible
			if m.configVisible {
				m.keepSelectionVisible()
			}
			return m, nil
		}
		// Refresh works from any surface (tree, input, config) so a clipper
		// import can be picked up; the bare r key is now the rename shortcut.
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
		if query, exact := m.fullTextFilter(); query == msg.query && exact == msg.exact {
			m.loading, m.err = false, msg.err
			if msg.err == nil {
				m.filtered = searchResultItems(m.items, msg.results, m.dateFilters, m.effectiveNameFilters(), m.hideNotes, m.mediaScope)
				m.sortFiltered()
				m.selected = 0
				m.keepSelectionVisible()
				m.applyPreviewContent()
				commands = append(commands, m.loadPreview())
			}
		}
	case documentsMsg:
		if msg.sequence == m.listSequence {
			m.loading, m.err = m.scanning, msg.err
			if msg.err == nil {
				previousID := ""
				if current, ok := m.selectedDocument(); ok {
					previousID = current.ID
				}
				isScanRefresh := msg.sequence == m.scanRefreshSequence
				knownDocuments := m.scanKnownDocuments
				m.items = documentItems(msg.documents)
				m.refreshFilter()
				if !isScanRefresh || !m.focusNewScanDocument(knownDocuments) {
					m.restoreSelection(previousID)
				}
				if isScanRefresh {
					m.scanRefreshSequence = 0
					m.scanKnownDocuments = nil
				}
				if query, exact := m.fullTextFilter(); query != "" {
					m.loading = true
					commands = append(commands, m.spinner.Tick, searchDocumentsCmd(m.ctx, m.app, query, exact))
				} else {
					commands = append(commands, m.loadPreview())
				}
			}
		}
	case previewDebounceMsg:
		if msg.generation == m.previewGen {
			if cmd := m.runPreviewLoad(msg.generation); cmd != nil {
				commands = append(commands, cmd)
			}
		}
	case previewMsg:
		// Ignore outdated async renders from a previous selection/width.
		if msg.generation != 0 && msg.generation != m.previewGen {
			break
		}
		m.err = msg.err
		if msg.err == nil {
			m.rawContent = msg.content
			if msg.documentID != "" {
				m.rawDocumentID = msg.documentID
			} else if document, ok := m.selectedDocument(); ok {
				m.rawDocumentID = document.ID
			}
			if msg.rendered != "" {
				m.renderedPreview = msg.rendered
				m.renderedWidth = msg.width
			} else {
				m.renderedPreview = ""
				m.renderedWidth = 0
			}
			// Cheap: paint cached ANSI; styling runs only in previewCmd.
			m.applyPreviewContent()
		}
	case scanMsg:
		m.scanning = false
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.captureScanRefresh()
			m.listSequence++
			m.scanRefreshSequence = m.listSequence
			m.loading = true
			m.scanStatusBase = formatScanStatus(msg.report)
			m.statusMessage = m.scanStatus(m.scanStatusBase)
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
			// Refresh both the preview and the tree so edits made in the editor
			// (new note via vim wq, or e-edit) are reflected in the file list.
			commands = append(commands, m.loadPreview(), listDocumentsCmd(m.ctx, m.app, m.listSequence))
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
	case summarizeDoneMsg:
		delete(m.summarizing, msg.documentID)
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = "summarize failed: " + shortID(msg.documentID)
		} else {
			m.applySummary(msg.documentID, msg.document.Summary)
			m.statusMessage = "summary saved: " + shortID(msg.documentID)
			if m.rawDocumentID == msg.documentID {
				m.rawContent = msg.document.Summary
				m.applyPreviewContent()
			}
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
				// The note is opened in the editor (vim), not a viewer: on wq the
				// standard edit-completion path runs (reindex + list refresh).
				return m, tea.ExecProcess(msg.command, func(err error) tea.Msg {
					return editorDoneMsg{selector: msg.document.ID, viewer: false, err: err}
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
			// Remove locally before the async reload. Keeping the deleted row's
			// numeric slot selects its next neighbor (or the previous row when it
			// was last), instead of restoreSelection falling back to pinned row 0.
			m.removeDeletedDocument(msg.documentID)
			m.statusMessage = "Moved " + filepath.Base(msg.path) + " to trash • restore: mm trash restore " + shortID(msg.documentID)
			if msg.trashBytes >= 256<<20 || msg.trashCount >= 200 {
				m.statusMessage += fmt.Sprintf(" • trash holds %d items (%s) — mm trash purge", msg.trashCount, formatBytesTUI(msg.trashBytes))
			}
			commands = append(commands, m.loadPreview(), listDocumentsCmd(m.ctx, m.app, m.listSequence))
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
			m.previewFocused = false
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
			if query, exact := m.fullTextFilter(); query != "" {
				commands = append(commands, m.threadSearchCmd(query, exact))
			}
		}
		// A dedicated result surface replaces the command palette rather than
		// stacking underneath it. This makes the next esc belong to the result.
		m.clearExecutedCommand()
		if msg.err == nil {
			m.hideInput()
		}
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
			if msg.virtual {
				m.applyDocumentTitle(msg.documentID, msg.title)
				m.statusMessage = "Renamed title: " + msg.title
			} else {
				m.statusMessage = "Renamed " + filepath.Base(msg.path)
			}
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
		m.clearExecutedCommand()
	case pdfConvertedMsg:
		m.loading, m.err = false, msg.err
		m.pdfConversionActive = false
		m.pdfConversionID = ""
		m.pdfProgressSequence++
		if msg.err == nil {
			verb := "updated"
			if msg.result.Created {
				verb = "created"
			}
			m.statusMessage = "PDF Markdown " + verb + ": " + shortID(msg.result.MarkdownDocument.ID)
			if len(msg.result.Chapters) != 0 {
				m.statusMessage += fmt.Sprintf(" · %d linked chapters", len(msg.result.Chapters))
			}
			m.hideInput()
			commands = append(commands, listDocumentsCmd(m.ctx, m.app, m.listSequence))
		}
		m.clearExecutedCommand()
	case pdfConversionProgressMsg:
		if m.pdfConversionActive && msg.sequence == m.pdfProgressSequence {
			m.pdfProgressStage = msg.progress.Stage
			m.pdfProgressDescription = msg.progress.Description
			if msg.progress.TotalPages > 0 {
				m.pdfProgressTotal = msg.progress.TotalPages
			}
			switch msg.progress.Stage {
			case "chunk_start":
				m.pdfProgressCompleted = max(0, msg.progress.PageFrom-1)
			case "chunk_done":
				m.pdfProgressCompleted = msg.progress.PageTo
			case "merge", "publish":
				m.pdfProgressCompleted = m.pdfProgressTotal
			}
		}
		commands = append(commands, waitPDFConversionCmd(msg.run))
	case pdfProgressTickMsg:
		if m.pdfConversionActive && msg.sequence == m.pdfProgressSequence {
			m.pdfProgressFrame++
			commands = append(commands, pdfProgressTickCmd(msg.sequence))
		}
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

func (m Model) selectedDocument() (membox.DocumentView, bool) {
	if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
		return membox.DocumentView{}, false
	}
	return m.filtered[m.selected].document, true
}

// previewDebounce is how long we wait after the last selection change before
// reading/rendering the document. Keeps arrow-key navigation fluid.
const previewDebounce = 60 * time.Millisecond

type previewDebounceMsg struct{ generation uint64 }

func (m *Model) loadPreview() tea.Cmd {
	m.previewGen++
	gen := m.previewGen
	// Debounce: only the latest generation after idle fires the real load.
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewDebounceMsg{generation: gen}
	})
}

func (m *Model) runPreviewLoad(gen uint64) tea.Cmd {
	if gen != m.previewGen {
		return nil
	}
	width := m.preview.Width
	if width <= 0 {
		_, width = m.layoutWidths()
	}
	if m.fullscreen && m.fullDocument != nil {
		return previewCmd(m.ctx, m.app, m.fullDocument.ID, width, gen)
	}
	if document, ok := m.selectedDocument(); ok {
		// Always resolve the media type before reading. In particular, never
		// send PDF binary bytes to a terminal: embedded control sequences can
		// corrupt the TUI in addition to rendering as mojibake.
		return previewCmd(m.ctx, m.app, document.ID, width, gen)
	}
	placeholder := previewPlaceholder("No document selected.")
	return func() tea.Msg {
		return previewMsg{
			content: placeholder, rendered: placeholder, width: width, generation: gen,
		}
	}
}

// applyPreviewContent paints the viewport from the cached render. It must stay
// cheap — Markdown styling runs only inside previewCmd / rewrapPreviewCmd.
func (m *Model) applyPreviewContent() {
	query := ""
	if m.inputVisible {
		query = textFilterQuery(m.input.Value())
	}
	if query == "" && len(m.textFilters) > 0 {
		query = m.textFilters[len(m.textFilters)-1].Value
	}
	rendered := m.renderedPreview
	if rendered == "" {
		rendered = m.rawContent // last resort: raw text, still no glamour here
	}
	content := highlightQuery(rendered, query)
	m.preview.SetContent(content)
	if line := firstMatchLine(rendered, query); line >= 0 {
		target := max(0, line-m.preview.Height/2)
		m.preview.SetYOffset(target)
		return
	}
	m.preview.GotoTop()
}

// rewrapPreviewCmd re-styles Markdown off the UI thread when the pane width changes.
func (m *Model) rewrapPreviewCmd() tea.Cmd {
	if strings.TrimSpace(m.rawContent) == "" {
		return nil
	}
	m.previewGen++
	gen := m.previewGen
	width := m.preview.Width
	if width <= 0 {
		_, width = m.layoutWidths()
	}
	raw := m.rawContent
	docID := m.rawDocumentID
	return func() tea.Msg {
		rendered := renderMarkdownPreview(raw, width)
		return previewMsg{
			documentID: docID, content: raw, rendered: rendered,
			width: width, generation: gen,
		}
	}
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

func (m *Model) resize() tea.Cmd {
	if m.fullscreen {
		m.preview.Width = max(20, m.width-2)
		m.preview.Height = max(3, m.height-2)
	} else {
		_, previewWidth := m.layoutWidths()
		m.preview.Width = previewWidth
		m.preview.Height = m.visibleRows()
	}
	m.input.Width = max(10, m.width-12)
	// Width change needs a new style pass — never on the UI goroutine.
	if m.rawContent != "" && m.preview.Width != m.renderedWidth {
		return m.rewrapPreviewCmd()
	}
	return nil
}

func (m Model) layoutWidths() (int, int) {
	if m.previewFocused {
		return 0, max(20, m.width)
	}
	if m.width >= 100 {
		list := max(32, m.width*2/5)
		return list, max(40, m.width-list-2)
	}
	list := max(24, m.width/2)
	return list, max(20, m.width-list-2)
}
