package tui

import (
	"strings"

	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/earendil-works/membox/internal/app"
)


type noteItem struct {
	UUID  string `json:"UUID"`
	Title string `json:"Title"`
}

// onboardingMsg signals that the workspace is empty and the user must add a
// notes directory before any other operation can proceed.
type onboardingMsg struct{}

// workspaceAddedMsg signals that the user successfully configured the first
// notes directory during onboarding.
type workspaceAddedMsg struct{}

const (
	modeNormal     = "normal"
	modeOnboarding = "onboarding"
)

type model struct {
	app *app.App

	mode         string
	viewport     viewport.Model
	input        textinput.Model
	width        int
	height       int
	completing   bool
	compIndex    int
	compOffset   int
	compFiltered []string
	compAll      []string
	history      []string
	historyIndex int
	historyTemp  string
	notes        []noteItem
}

func newModel(a *app.App) *model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = ""
	ti.Focus()
	ti.CharLimit = 256
	ti.ShowSuggestions = false

	vp := viewport.New(80, 20)
	vp.SetContent("")

	m := &model{
		app:     a,
		mode:    modeNormal,
		viewport: vp,
		input:   ti,
		compAll: completionCandidates(),
	}

	if a.Workspaces().IsEmpty() {
		m.mode = modeOnboarding
	}

	return m
}

func (m *model) Init() tea.Cmd {
	if m.mode == modeOnboarding {
		return tea.Batch(
			textinput.Blink,
			func() tea.Msg { return onboardingMsg{} },
		)
	}
	return tea.Batch(
		textinput.Blink,
		loadInitialNotes(m.app),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()
	case onboardingMsg:
		m.appendOutput("Welcome to membox.\n\nNo notes directories are configured yet.\nEnter the path to your first notes directory below, or press q to quit.")
	case workspaceAddedMsg:
		m.mode = modeNormal
		m.appendOutput("Workspace configured. Loading notes...")
		cmds = append(cmds, loadInitialNotes(m.app))
	case notesMsg:
		m.notes = msg
	case outputMsg:
		m.appendOutput(string(msg))
		cmds = append(cmds, loadInitialNotes(m.app))
	case errorMsg:
		m.appendOutput(string(msg))
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

		if m.mode == modeOnboarding {
			switch msg.Type {
			case tea.KeyEnter:
				return m, m.submitOnboarding()
			case tea.KeyCtrlC, tea.KeyEsc:
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}

		return m.handleNormalKey(msg)
	}

	var cmd tea.Cmd
	oldVal := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	if m.input.Value() != oldVal {
		m.completing = false
		m.historyTemp = m.input.Value()
		m.updateLayout()
	}

	if _, ok := msg.(tea.KeyMsg); !ok {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.completing {
		switch msg.Type {
		case tea.KeyUp:
			if m.compIndex > 0 {
				m.compIndex--
				m.ensureCompletionVisible()
			}
			return m, nil
		case tea.KeyDown:
			if m.compIndex < len(m.compFiltered)-1 {
				m.compIndex++
				m.ensureCompletionVisible()
			}
			return m, nil
		case tea.KeyTab, tea.KeyEnter:
			if len(m.compFiltered) > 0 {
				m.input.SetValue(m.compFiltered[m.compIndex])
				m.input.CursorEnd()
			}
			m.completing = false
			m.updateLayout()
			return m, nil
		case tea.KeyEsc:
			m.completing = false
			m.updateLayout()
			return m, nil
		default:
			m.completing = false
			m.updateLayout()
		}
	}

	switch msg.Type {
	case tea.KeyTab:
		m.showCompletions()
		return m, nil
	case tea.KeyUp:
		m.historyPrev()
		return m, nil
	case tea.KeyDown:
		m.historyNext()
		return m, nil
	case tea.KeyPgUp:
		m.viewport.LineUp(3)
		return m, nil
	case tea.KeyPgDown:
		m.viewport.LineDown(3)
		return m, nil
	case tea.KeyEnter:
		return m, m.submitInput()
	}

	var cmd tea.Cmd
	oldVal := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != oldVal {
		m.completing = false
		m.historyTemp = m.input.Value()
		m.updateLayout()
	}
	return m, cmd
}

func (m *model) submitOnboarding() tea.Cmd {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return nil
	}
	return func() tea.Msg {
		if err := m.app.AddWorkspace(input); err != nil {
			return errorMsg(fmt.Sprintf("add workspace: %v", err))
		}
		return workspaceAddedMsg{}
	}
}

func (m *model) submitInput() tea.Cmd {
	input := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	m.completing = false
	m.historyTemp = ""
	m.historyIndex = len(m.history)
	if input != "" {
		m.history = append(m.history, input)
		m.historyIndex = len(m.history)
	}
	if input == "" {
		return nil
	}
	if input == "?" || input == "h" || input == "help" {
		m.appendOutput(helpText)
		return loadInitialNotes(m.app)
	}

	cmd, err := parseCommand(input)
	if err != nil {
		return func() tea.Msg { return errorMsg(err.Error()) }
	}

	switch cmd.kind {
	case cmdQuit:
		return tea.Quit
	case cmdUnknown:
		if cmd.help != "" {
			return func() tea.Msg { return errorMsg(cmd.help) }
		}
		return func() tea.Msg { return errorMsg("unknown command") }
	}

	return runCommand(m.app, cmd)
}

func (m *model) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.historyIndex == len(m.history) {
		m.historyTemp = m.input.Value()
	}
	if m.historyIndex > 0 {
		m.historyIndex--
		m.input.SetValue(m.history[m.historyIndex])
		m.input.CursorEnd()
	}
}

func (m *model) historyNext() {
	if m.historyIndex < len(m.history) {
		m.historyIndex++
	}
	if m.historyIndex == len(m.history) {
		m.input.SetValue(m.historyTemp)
		m.input.CursorEnd()
		return
	}
	m.input.SetValue(m.history[m.historyIndex])
	m.input.CursorEnd()
}

func (m *model) updateLayout() {
	inputHeight := 3
	completionHeight := 0
	if m.completing {
		completionHeight = min(len(m.compFiltered), 8) + 2
	}
	m.viewport.Width = m.width
	m.viewport.Height = m.height - 1 - inputHeight - completionHeight
	m.input.Width = m.width - 4
}

func (m *model) showCompletions() {
	input := m.input.Value()
	m.compFiltered = progressiveCompletions(input, m.notes)
	m.compIndex = 0
	m.compOffset = 0
	m.completing = len(m.compFiltered) > 0
	m.updateLayout()
}

func (m *model) ensureCompletionVisible() {
	maxVisible := min(len(m.compFiltered), 8)
	if m.compIndex < m.compOffset {
		m.compOffset = m.compIndex
	}
	if m.compIndex >= m.compOffset+maxVisible {
		m.compOffset = m.compIndex - maxVisible + 1
	}
}

func (m *model) appendOutput(s string) {
	rendered, err := renderOutput(s, m.width-4)
	if err != nil {
		rendered = s
	}
	current := m.viewport.View()
	if current == "" {
		m.viewport.SetContent(rendered)
	} else {
		m.viewport.SetContent(current + "\n\n" + rendered)
	}
	m.viewport.GotoBottom()
}

func (m *model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	viewportBox := lipgloss.NewStyle().
		MarginLeft(2).
		Width(m.width - 4).
		Render(m.viewport.View())

	var parts []string
	parts = append(parts, viewportBox)
	parts = append(parts, m.renderInputBox())

	if m.completing && len(m.compFiltered) > 0 {
		parts = append(parts, m.renderCompletionBox())
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *model) renderInputBox() string {
	return lipgloss.NewStyle().
		BorderStyle(horizontalBorder).
		BorderForeground(lipgloss.Color("#5c6370")).
		Width(m.width - 2).
		Render(m.input.View())
}

func (m *model) renderCompletionBox() string {
	style := lipgloss.NewStyle().
		BorderStyle(horizontalBorder).
		BorderForeground(lipgloss.Color("#5c6370")).
		Width(m.width - 2)

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#3e4451")).
		Foreground(lipgloss.Color("#abb2bf"))

	var lines []string
	maxItems := min(len(m.compFiltered), 8)
	end := min(len(m.compFiltered), m.compOffset+maxItems)
	for i := m.compOffset; i < end; i++ {
		item := m.compFiltered[i]
		if i == m.compIndex {
			item = selectedStyle.Render("  " + item + "  ")
		} else {
			item = "  " + item
		}
		lines = append(lines, item)
	}
	return style.Render(strings.Join(lines, "\n"))
}

var horizontalBorder = lipgloss.Border{
	Top:         "─",
	Bottom:      "─",
	Left:        "",
	Right:       "",
	TopLeft:     "",
	TopRight:    "",
	BottomLeft:  "",
	BottomRight: "",
}

const helpText = `Commands:
  note ls                           list recent notes
  note search <query>               full-text search note titles
  workspace add <path>              add a notes directory
  workspace rm <path>               remove a notes directory
  workspace ls                      list configured directories
  q                                 quit
  ?                                 show this help`
