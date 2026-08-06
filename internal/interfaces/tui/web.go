package tui

// Web Companion control surfaces inside the TUI: the permanent status badge,
// the ctrl+o panel section, the :web command family, and the quit-time
// lifecycle prompt. The TUI never embeds the server; it probes, spawns, and
// stops the companion process.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"membox"
	"membox/internal/application"
)

// webState mirrors the companion control plane for rendering.
type webState struct {
	status membox.WebStatusView
	// starting covers the window between spawn and the first status answer.
	starting bool
	// err marks the last ensure/probe failure ("WEB ! unavailable").
	err error
	// spawnedPID remembers the companion this TUI session started, so quit
	// policies only stop what they own.
	spawnedPID int
	// sequence guards the periodic refresh loop against duplicates.
	sequence uint64
	// controllerID owns one renewable lease; it is never shared by another TUI.
	controllerID string
}

func (w webState) running() bool { return w.status.Running }

// owned reports that this TUI session started the companion now running.
func (w webState) owned() bool { return w.spawnedPID != 0 && w.spawnedPID == w.status.PID }

type webStatusMsg struct {
	view     membox.WebStatusView
	err      error
	sequence uint64
	refresh  bool
}

const webRefreshInterval = 3 * time.Second

func newWebControllerID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("tui-%d", time.Now().UnixNano())
}

func webEnsureCmd(ctx context.Context, app App, controller string) tea.Cmd {
	return func() tea.Msg {
		// TUI entry restarts the companion so an orphaned mm serve / ephemeral
		// fallback from a previous session cannot steal :8787 from the extension
		// or leave Agent workers attached to a dead control plane.
		view, err := app.RestartWebCompanion(ctx, "")
		if err == nil && view.Running {
			err = app.RenewWebLease(ctx, controller)
		}
		return webStatusMsg{view: view, err: err}
	}
}

func webRefreshCmd(ctx context.Context, app App, controller string, sequence uint64) tea.Cmd {
	return func() tea.Msg {
		view, err := app.WebStatus(ctx)
		if err == nil && view.Running {
			err = app.RenewWebLease(ctx, controller)
		}
		return webStatusMsg{view: view, err: err, sequence: sequence, refresh: true}
	}
}

type webQuitResultMsg struct {
	action     string
	err        error
	fromPrompt bool
	notify     bool
	url        string
}

// webStopAndQuitCmd is an explicit global stop. The TUI only quits after the
// companion confirms shutdown; failures restore the prompt.
func webStopAndQuitCmd(ctx context.Context, app App, fromPrompt bool) tea.Cmd {
	return func() tea.Msg {
		return webQuitResultMsg{action: "stop", err: app.StopWeb(ctx), fromPrompt: fromPrompt}
	}
}

// webReleaseAndQuitCmd releases only this TUI's lease. A session companion
// exits when it was the last controller; another TUI's lease keeps it alive.
func webReleaseAndQuitCmd(ctx context.Context, app App, controller string, fromPrompt bool) tea.Cmd {
	return func() tea.Msg {
		return webQuitResultMsg{action: "release", err: app.ReleaseWebLease(ctx, controller), fromPrompt: fromPrompt}
	}
}

// webKeepAndQuitCmd promotes first, then releases this TUI lease. Neither error
// is ignored: an unconfirmed keep must never be followed by process exit.
func webKeepAndQuitCmd(ctx context.Context, app App, controller string, notify, fromPrompt bool, url string) tea.Cmd {
	return func() tea.Msg {
		if err := app.SetWebLifecycle(ctx, "keep"); err != nil {
			return webQuitResultMsg{action: "keep", err: err, fromPrompt: fromPrompt}
		}
		if err := app.ReleaseWebLease(ctx, controller); err != nil {
			return webQuitResultMsg{action: "release", err: err, fromPrompt: fromPrompt}
		}
		return webQuitResultMsg{action: "keep", fromPrompt: fromPrompt, notify: notify, url: url}
	}
}

func webStopCmd(ctx context.Context, app App) tea.Cmd {
	return func() tea.Msg {
		err := app.StopWeb(ctx)
		if err != nil {
			return commandResultMsg{err: err}
		}
		return commandResultMsg{text: "web companion stopped"}
	}
}

// webCommandAction dispatches the :web command family.
func (m Model) webCommandAction(tokens []string) (func() tea.Msg, string, error) {
	if len(tokens) < 2 {
		return nil, "web <status|open|start|stop|keep>", fmt.Errorf("web subcommand is required")
	}
	switch tokens[1] {
	case "status":
		return func() tea.Msg {
			view, err := m.app.WebStatus(m.ctx)
			return webStatusMsg{view: view, err: err}
		}, "web status", nil
	case "open":
		selector := strings.Join(tokens[2:], " ")
		return func() tea.Msg {
			var url string
			var err error
			if selector != "" {
				url, err = m.app.OpenDocumentWeb(m.ctx, selector)
			} else {
				var view membox.WebStatusView
				view, err = m.app.EnsureWebCompanion(m.ctx, "")
				url = view.URL
			}
			if err != nil {
				return openWebMsg{err: err}
			}
			command, openErr := m.launcher.OpenCommand(m.ctx, url)
			if openErr == nil {
				openErr = command.Run()
			}
			return openWebMsg{url: url, err: openErr}
		}, "web open [document]", nil
	case "start":
		return func() tea.Msg {
			view, err := m.app.EnsureWebCompanion(m.ctx, "")
			return webStatusMsg{view: view, err: err}
		}, "web start", nil
	case "stop":
		return webStopCmd(m.ctx, m.app), "web stop", nil
	case "keep":
		return func() tea.Msg {
			if err := m.app.SetSetting(m.ctx, application.SettingWebOnExit, application.OnExitKeep); err != nil {
				return commandResultMsg{err: err}
			}
			// Promote a running session companion so it actually survives.
			if view, statusErr := m.app.WebStatus(m.ctx); statusErr != nil {
				return commandResultMsg{err: statusErr}
			} else if view.Running {
				if lifecycleErr := m.app.SetWebLifecycle(m.ctx, "keep"); lifecycleErr != nil {
					return commandResultMsg{err: lifecycleErr}
				}
			}
			return settingSavedMsg{key: application.SettingWebOnExit, value: application.OnExitKeep}
		}, "web keep", nil
	}
	return nil, "web <status|open|start|stop|keep>", fmt.Errorf("unknown web subcommand %q", tokens[1])
}

// applyWebStatus folds one status answer into the model and keeps the refresh
// loop alive exactly once while the companion runs.
func (m *Model) applyWebStatus(msg webStatusMsg) tea.Cmd {
	if msg.refresh && msg.sequence != m.web.sequence {
		return nil // stale loop superseded by a newer ensure/stop
	}
	if msg.err != nil {
		m.web.err = msg.err
		m.web.starting = false
		return nil
	}
	m.web.err = nil
	m.web.starting = false
	m.web.status = msg.view
	if msg.view.Started {
		m.web.spawnedPID = msg.view.PID
	}
	if !msg.view.Running {
		return nil
	}
	if !msg.refresh {
		m.web.sequence++
	}
	// Schedule exactly one follow-up probe; the sequence check above prunes
	// superseded loops.
	sequence := m.web.sequence
	ctx, app, controller := m.ctx, m.app, m.web.controllerID
	return tea.Tick(webRefreshInterval, func(time.Time) tea.Msg {
		view, err := app.WebStatus(ctx)
		if err == nil && view.Running {
			err = app.RenewWebLease(ctx, controller)
		}
		return webStatusMsg{view: view, err: err, sequence: sequence, refresh: true}
	})
}

// beginQuit implements the ctrl+d lifecycle policy.
func (m Model) beginQuit() (tea.Model, tea.Cmd) {
	if m.webQuitPrompt {
		return m, nil
	}
	if !m.web.running() {
		return m, tea.Quit
	}
	switch m.webOnExitPolicy() {
	case application.OnExitStop:
		// Quit policy releases only this TUI. Another TUI's lease must keep the
		// shared session companion alive.
		return m, webReleaseAndQuitCmd(m.ctx, m.app, m.web.controllerID, false)
	case application.OnExitKeep:
		notify := m.web.status.Tabs > 0 && m.web.owned() && m.web.status.Mode != "keep"
		return m, webKeepAndQuitCmd(m.ctx, m.app, m.web.controllerID, notify, false, m.web.status.URL)
	default: // ask
		m.webQuitPrompt = true
		return m, nil
	}
}

// updateWebQuitPrompt handles the modal k/s/esc choice.
func (m Model) updateWebQuitPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "k":
		m.webQuitPrompt = false
		notify := m.web.status.Tabs > 0 && m.web.owned()
		return m, webKeepAndQuitCmd(m.ctx, m.app, m.web.controllerID, notify, true, m.web.status.URL)
	case "s":
		m.webQuitPrompt = false
		return m, webStopAndQuitCmd(m.ctx, m.app, true)
	case "esc":
		m.webQuitPrompt = false
	}
	return m, nil
}

// webOnExitPolicy reads the stored quit policy, defaulting to ask.
func (m Model) webOnExitPolicy() string {
	for _, setting := range m.settings {
		if setting.Key == application.SettingWebOnExit {
			return setting.Value
		}
	}
	return application.OnExitAsk
}

var (
	webOKStyle   = lipgloss.NewStyle().Foreground(colors.Success)
	webWarnStyle = lipgloss.NewStyle().Foreground(colors.Warning)
)

// webBadge renders the permanent status-bar block.
func (m Model) webBadge() string {
	switch {
	case m.web.err != nil:
		return errorStyle.Render("WEB ! unavailable")
	case m.web.starting:
		return dimStyle.Render("WEB ◐ starting")
	case !m.web.running():
		return dimStyle.Render("WEB ○ off")
	}
	status := m.web.status
	label := fmt.Sprintf("WEB ● :%d", status.Port)
	if status.Tabs == 0 {
		return webOKStyle.Render(label)
	}
	label += fmt.Sprintf(" · %d tab%s", status.Tabs, pluralS(status.Tabs))
	if status.DirtyTabs > 0 {
		label += fmt.Sprintf(" · %d unsaved", status.DirtyTabs)
		return webWarnStyle.Render(label)
	}
	label += " · saved"
	return webOKStyle.Render(label)
}

func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// webPanelRows is the line budget the ctrl+o web section reserves.
func (m Model) webPanelRows() int { return 4 }

// webPanelLines renders the ctrl+o web reader section (fixed height).
func (m Model) webPanelLines(width int) []string {
	var statusLine, tabsLine string
	switch {
	case m.web.err != nil:
		statusLine = errorStyle.Render("! unavailable — x retries")
	case m.web.starting:
		statusLine = dimStyle.Render("◐ starting…")
	case !m.web.running():
		statusLine = dimStyle.Render("○ off — x starts it")
	default:
		status := m.web.status
		dot := webOKStyle.Render("●")
		if status.DirtyTabs > 0 {
			dot = webWarnStyle.Render("●")
		}
		statusLine = fmt.Sprintf("%s running :%d · %s · started %s", dot, status.Port, status.Mode, formatAgo(status.StartedAt))
	}
	if m.web.running() && m.web.status.Tabs > 0 {
		tabsLine = fmt.Sprintf("%d tab%s connected", m.web.status.Tabs, pluralS(m.web.status.Tabs))
		if m.web.status.DirtyTabs > 0 {
			tabsLine = webWarnStyle.Render(fmt.Sprintf("%s · %d unsaved", tabsLine, m.web.status.DirtyTabs))
		} else {
			tabsLine += " · all saved"
		}
	} else {
		tabsLine = dimStyle.Render("no browser tabs")
	}
	return []string{
		accentStyle.Render("web reader"),
		fitWidth("  "+statusLine, width),
		fitWidth("  "+tabsLine, width),
		dimStyle.Render("  o open reader • x stop/start"),
	}
}

func formatAgo(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "unknown"
	}
	elapsed := time.Since(startedAt)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm ago", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	}
}

// webQuitPromptView renders the quit-time confirmation block.
func (m Model) webQuitPromptView() string {
	width := max(10, m.width-2)
	border := lipgloss.NewStyle().Width(width).MaxWidth(width).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(colors.BorderAccent)
	var lines []string
	if m.web.status.Tabs > 0 {
		lines = append(lines, fmt.Sprintf("Web Reader is open in %d browser tab%s.", m.web.status.Tabs, pluralS(m.web.status.Tabs)))
		if m.web.status.DirtyTabs > 0 {
			lines = append(lines, webWarnStyle.Render(fmt.Sprintf("%d tab%s has unsaved notes.", m.web.status.DirtyTabs, pluralS(m.web.status.DirtyTabs))))
		}
	} else {
		lines = append(lines, "Web Reader is active.")
	}
	lines = append(lines, "")
	lines = append(lines, "[k] Keep web running and quit")
	lines = append(lines, "[s] Stop web and quit")
	lines = append(lines, dimStyle.Render("esc cancel"))
	return border.Render(strings.Join(lines, "\n"))
}

// notifyWebKeptRunning sends a best-effort macOS notification so a kept
// companion is never invisible.
func notifyWebKeptRunning(url string) {
	if runtime.GOOS != "darwin" {
		return
	}
	message := strings.ReplaceAll("membox Web Reader remains active on "+url+". Run mm web stop to stop it.", `"`, `'`)
	command := exec.Command("osascript", "-e", `display notification "`+message+`" with title "membox"`)
	_ = command.Start()
}
