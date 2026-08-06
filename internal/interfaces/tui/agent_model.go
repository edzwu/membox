package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

// agentUIState holds TUI-local Agent workspace state. It is independent of the
// filter/command input fields and of the document preview viewport.
type agentUIState struct {
	clientID string
	client   *AgentHTTP
	status   agentStatusPayload
	badge    string // OFF | CONNECTING | READY | RUNNING | ERROR

	sessionID     string
	sessionTitle  string
	lastEventID   string
	runID         string
	documentID    string // attached on next prompt
	documentTitle string

	// transcript lines (simple V1 rendering)
	lines []agentLine

	// stream control
	sequence   uint64
	streamSeq  uint64
	cancelSSE  context.CancelFunc
	events     <-chan agentEventMsg
	streamErrs <-chan error

	viewport viewport.Model
	ready    bool
	follow   bool
	err      error
}

type agentLine struct {
	Kind    string // user|assistant|tool|system|error
	Text    string
	Running bool
}

type agentReadyMsg struct {
	Sequence uint64
	Client   *AgentHTTP
	Status   agentStatusPayload
	Err      error
}

type agentSessionMsg struct {
	Sequence uint64
	Snap     agentSnapshotPayload
	Err      error
}

type agentPromptMsg struct {
	Sequence uint64
	RunID    string
	Err      error
}

type agentEventMsg struct {
	Sequence  uint64
	SessionID string
	Event     agentEventPayload
}

type agentStreamReadyMsg struct {
	Sequence  uint64
	SessionID string
	Events    <-chan agentEventMsg
	Errs      <-chan error
	First     agentEventMsg
}

type agentErrMsg struct {
	Sequence uint64
	Err      error
}

func newAgentUIState() agentUIState {
	return agentUIState{
		clientID: newAgentClientID(),
		badge:    "OFF",
		follow:   true,
		viewport: viewport.New(0, 0),
	}
}

func newAgentClientID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "tui-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("tui-%d", time.Now().UnixNano())
}

func (a *agentUIState) setBadge() {
	if a.err != nil {
		a.badge = "ERROR"
		return
	}
	if a.client == nil {
		if a.badge == "CONNECTING" {
			return
		}
		a.badge = "OFF"
		return
	}
	if a.runID != "" {
		a.badge = "RUNNING"
		return
	}
	if a.status.Available {
		a.badge = "READY"
		return
	}
	a.badge = "CONNECTING"
}

func (a *agentUIState) appendLine(kind, text string) {
	a.lines = append(a.lines, agentLine{Kind: kind, Text: text})
	// Bound memory.
	if len(a.lines) > 500 {
		a.lines = a.lines[len(a.lines)-500:]
	}
}

func (a *agentUIState) appendDelta(text string) {
	if text == "" {
		return
	}
	n := len(a.lines)
	if n > 0 && a.lines[n-1].Kind == "assistant" && a.lines[n-1].Running {
		a.lines[n-1].Text += text
		return
	}
	a.lines = append(a.lines, agentLine{Kind: "assistant", Text: text, Running: true})
}

func (a *agentUIState) finishAssistant() {
	n := len(a.lines)
	if n > 0 && a.lines[n-1].Kind == "assistant" {
		a.lines[n-1].Running = false
	}
}

func (a *agentUIState) renderTranscript() string {
	if len(a.lines) == 0 {
		return "No messages yet. Ask a question about your documents."
	}
	var b strings.Builder
	for _, line := range a.lines {
		switch line.Kind {
		case "user":
			b.WriteString("You: ")
		case "assistant":
			b.WriteString("Agent: ")
		case "tool":
			b.WriteString("Tool: ")
		case "system":
			b.WriteString("· ")
		case "error":
			b.WriteString("Error: ")
		}
		b.WriteString(line.Text)
		if line.Running {
			b.WriteString(" ▍")
		}
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *agentUIState) stopStream() {
	if a.cancelSSE != nil {
		a.cancelSSE()
		a.cancelSSE = nil
	}
	a.events = nil
	a.streamErrs = nil
}

func (m Model) handleAgentReady(msg agentReadyMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.sequence {
		return m, nil
	}
	if msg.Err != nil {
		m.agent.err = msg.Err
		m.agent.client = msg.Client
		m.agent.status = msg.Status
		m.agent.setBadge()
		m.filterErr = msg.Err
		return m, nil
	}
	m.agent.client = msg.Client
	m.agent.status = msg.Status
	m.agent.err = nil
	m.agent.setBadge()
	// Restore last session or create.
	return m, m.agentBootstrapSessionCmd()
}

func (m Model) agentBootstrapSessionCmd() tea.Cmd {
	client := m.agent.client
	seq := m.agent.sequence
	existing := m.agent.sessionID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if existing != "" {
			snap, err := client.Snapshot(ctx, existing)
			if err == nil {
				return agentSessionMsg{Sequence: seq, Snap: snap}
			}
		}
		sessions, err := client.ListSessions(ctx)
		if err == nil && len(sessions) > 0 {
			snap, err := client.Snapshot(ctx, sessions[0].ID)
			if err == nil {
				return agentSessionMsg{Sequence: seq, Snap: snap}
			}
		}
		snap, err := client.CreateSession(ctx, "TUI chat")
		return agentSessionMsg{Sequence: seq, Snap: snap, Err: err}
	}
}

func (m Model) handleAgentSession(msg agentSessionMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.sequence {
		return m, nil
	}
	if msg.Err != nil {
		m.agent.err = msg.Err
		m.agent.setBadge()
		m.filterErr = msg.Err
		return m, nil
	}
	m.agent.sessionID = msg.Snap.Session.ID
	m.agent.sessionTitle = msg.Snap.Session.Title
	m.agent.lastEventID = msg.Snap.LastEventID
	m.agent.err = nil
	// Seed transcript from messages (best-effort text extraction).
	if len(m.agent.lines) == 0 {
		for _, raw := range msg.Snap.Messages {
			if line, ok := summarizeAgentMessage(raw); ok {
				m.agent.appendLine(line.Kind, line.Text)
			}
		}
	}
	m.agent.setBadge()
	m.refreshAgentViewport()
	cmd := m.beginAgentStream()
	return m, cmd
}

// beginAgentStream cancels any prior SSE reader and starts a new one.
// Must be called on the Model value that will be returned from Update.
func (m *Model) beginAgentStream() tea.Cmd {
	if m.agent.client == nil || m.agent.sessionID == "" {
		return nil
	}
	m.agent.stopStream()
	m.agent.streamSeq++
	seq := m.agent.streamSeq
	ctx, cancel := context.WithCancel(m.ctx)
	m.agent.cancelSSE = cancel
	return m.agent.client.SubscribeSSE(ctx, m.agent.sessionID, m.agent.lastEventID, seq)
}

func (m Model) handleAgentStreamReady(msg agentStreamReadyMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.streamSeq || msg.SessionID != m.agent.sessionID {
		return m, nil
	}
	m.agent.events = msg.Events
	m.agent.streamErrs = msg.Errs
	var cmds []tea.Cmd
	next, cmd := m.handleAgentEvent(msg.First)
	m = next
	if cmd != nil {
		cmds = append(cmds, cmd)
	} else {
		cmds = append(cmds, waitAgentEventCmd(msg.Sequence, msg.SessionID, msg.Events, msg.Errs))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleAgentEvent(msg agentEventMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.streamSeq || msg.SessionID != m.agent.sessionID {
		return m, waitAgentEventCmd(m.agent.streamSeq, m.agent.sessionID, m.agent.events, m.agent.streamErrs)
	}
	m.agent.lastEventID = msg.Event.ID
	switch msg.Event.Type {
	case "assistant.delta":
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(msg.Event.Payload, &p)
		m.agent.appendDelta(p.Text)
	case "assistant.completed":
		m.agent.finishAssistant()
	case "run.started":
		m.agent.runID = msg.Event.RunID
		m.agent.setBadge()
	case "run.settled", "run.interrupted":
		m.agent.runID = ""
		m.agent.finishAssistant()
		m.agent.setBadge()
	case "tool.started":
		var p struct {
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal(msg.Event.Payload, &p)
		m.agent.appendLine("tool", p.Tool+"…")
	case "tool.completed":
		var p struct {
			Tool   string `json:"tool"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(msg.Event.Payload, &p)
		m.agent.appendLine("tool", fmt.Sprintf("%s (%s)", p.Tool, p.Status))
	case "agent.error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(msg.Event.Payload, &p)
		if p.Message != "" {
			m.agent.appendLine("error", p.Message)
		}
	case "stream.reset":
		m.agent.appendLine("system", "stream reset — refreshing session")
		return m, m.agentBootstrapSessionCmd()
	}
	m.refreshAgentViewport()
	return m, waitAgentEventCmd(msg.Sequence, msg.SessionID, m.agent.events, m.agent.streamErrs)
}

func (m Model) handleAgentErr(msg agentErrMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.streamSeq && msg.Sequence != m.agent.sequence {
		return m, nil
	}
	m.agent.err = msg.Err
	m.agent.setBadge()
	if msg.Err != nil {
		m.agent.appendLine("error", msg.Err.Error())
		m.refreshAgentViewport()
	}
	return m, nil
}

func (m Model) submitAgentPrompt() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if m.agent.client == nil || m.agent.sessionID == "" {
		m.filterErr = fmt.Errorf("agent is not ready")
		return m, nil
	}
	if m.agent.runID != "" {
		m.filterErr = fmt.Errorf("agent is busy — wait or abort")
		return m, nil
	}
	m.agent.appendLine("user", text)
	m.input.SetValue("")
	m.filterErr = nil
	m.refreshAgentViewport()
	client := m.agent.client
	sessionID := m.agent.sessionID
	docID := m.agent.documentID
	seq := m.agent.sequence
	idem := newIdempotencyKey()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runID, err := client.Prompt(ctx, sessionID, text, docID, idem)
		return agentPromptMsg{Sequence: seq, RunID: runID, Err: err}
	}
}

func (m Model) handleAgentPrompt(msg agentPromptMsg) (Model, tea.Cmd) {
	if msg.Sequence != m.agent.sequence {
		return m, nil
	}
	if msg.Err != nil {
		m.agent.err = msg.Err
		m.agent.appendLine("error", msg.Err.Error())
		m.agent.setBadge()
		m.filterErr = msg.Err
		m.refreshAgentViewport()
		return m, nil
	}
	m.agent.runID = msg.RunID
	m.agent.setBadge()
	return m, nil
}

func (m *Model) refreshAgentViewport() {
	content := m.agent.renderTranscript()
	m.agent.viewport.SetContent(content)
	if m.agent.follow {
		m.agent.viewport.GotoBottom()
	}
}

func (m Model) abortAgentRun() (Model, tea.Cmd) {
	if m.agent.client == nil || m.agent.sessionID == "" || m.agent.runID == "" {
		return m, nil
	}
	client := m.agent.client
	sessionID := m.agent.sessionID
	runID := m.agent.runID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.Abort(ctx, sessionID, runID)
		if err != nil {
			return agentErrMsg{Sequence: m.agent.sequence, Err: err}
		}
		return agentErrMsg{Sequence: m.agent.sequence, Err: nil}
	}
}

func newIdempotencyKey() string {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id.String()
}

// agentStatusBarBadge renders the agent connection/run state in the permanent
// status bar, next to the WEB badge. The input-field mode badge stays a plain
// "AGENT" mode indicator; live state belongs in the status bar. It stays
// hidden until the agent is actually used so the bar stays quiet.
func (m Model) agentStatusBarBadge() string {
	if m.agent.badge == "OFF" && m.agent.client == nil && m.inputMode != inputModeAgent {
		return ""
	}
	switch m.agent.badge {
	case "ERROR":
		label := "error"
		if m.agent.err != nil {
			label = m.agent.err.Error()
		}
		if runes := []rune(label); len(runes) > 40 {
			label = string(runes[:37]) + "…"
		}
		return "  " + errorStyle.Render("AGENT ! "+label)
	case "CONNECTING":
		return "  " + dimStyle.Render("AGENT ◐ connecting")
	case "RUNNING":
		return "  " + webWarnStyle.Render("AGENT ● running")
	case "READY":
		return "  " + webOKStyle.Render("AGENT ● ready")
	default:
		return "  " + dimStyle.Render("AGENT ○ off")
	}
}

func summarizeAgentMessage(raw json.RawMessage) (agentLine, bool) {
	var msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return agentLine{}, false
	}
	text := extractMessageText(msg.Content)
	if text == "" {
		return agentLine{}, false
	}
	switch msg.Role {
	case "user":
		return agentLine{Kind: "user", Text: text}, true
	case "assistant":
		return agentLine{Kind: "assistant", Text: text}, true
	default:
		return agentLine{}, false
	}
}

func extractMessageText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// content may be a string or array of blocks.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" || block.Type == "" {
				b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return ""
}
