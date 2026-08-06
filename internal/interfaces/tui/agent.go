package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
)

// AgentHTTP is the Companion-facing Agent client used by the TUI.
type AgentHTTP struct {
	BaseURL  string
	Token    string
	ClientID string
	HTTP     *http.Client
}

func newAgentHTTP(baseURL, token, clientID string) *AgentHTTP {
	return &AgentHTTP{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		ClientID: clientID,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

type agentStatusPayload struct {
	Enabled   bool    `json:"enabled"`
	Available bool    `json:"available"`
	State     string  `json:"state"`
	Error     *string `json:"error"`
}

type agentSessionPayload struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	State         string `json:"state"`
	ThinkingLevel string `json:"thinking_level"`
}

type agentSnapshotPayload struct {
	Session     agentSessionPayload `json:"session"`
	LastEventID string              `json:"last_event_id"`
	StreamEpoch string              `json:"stream_epoch"`
	Messages    []json.RawMessage   `json:"messages"`
}

type agentEventPayload struct {
	V         int             `json:"v"`
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Type      string          `json:"type"`
	At        time.Time       `json:"at"`
	Payload   json.RawMessage `json:"payload"`
}

func (c *AgentHTTP) Status(ctx context.Context) (agentStatusPayload, error) {
	var out agentStatusPayload
	err := c.getJSON(ctx, "/api/agent/status", &out)
	return out, err
}

func (c *AgentHTTP) CreateSession(ctx context.Context, title string) (agentSnapshotPayload, error) {
	var out agentSnapshotPayload
	err := c.postJSON(ctx, "/api/agent/sessions", map[string]any{"title": title}, &out, true)
	return out, err
}

func (c *AgentHTTP) ListSessions(ctx context.Context) ([]agentSessionPayload, error) {
	var out struct {
		Sessions []agentSessionPayload `json:"sessions"`
	}
	err := c.getJSON(ctx, "/api/agent/sessions", &out)
	return out.Sessions, err
}

func (c *AgentHTTP) Snapshot(ctx context.Context, sessionID string) (agentSnapshotPayload, error) {
	var out agentSnapshotPayload
	err := c.getJSON(ctx, "/api/agent/sessions/"+sessionID, &out)
	return out, err
}

func (c *AgentHTTP) Prompt(ctx context.Context, sessionID, text, documentID, idem string) (string, error) {
	body := map[string]any{"text": text}
	if documentID != "" {
		body["context"] = map[string]any{"document_id": documentID}
	}
	var out struct {
		RunID    string `json:"run_id"`
		Accepted bool   `json:"accepted"`
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/agent/sessions/"+sessionID+"/messages", body)
	if err != nil {
		return "", err
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	return out.RunID, nil
}

func (c *AgentHTTP) Abort(ctx context.Context, sessionID, runID string) error {
	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/api/agent/sessions/%s/runs/%s/abort", sessionID, runID), map[string]any{})
	if err != nil {
		return err
	}
	return c.doJSON(req, &map[string]any{})
}

// SubscribeSSE starts a background reader that emits agentEventMsg values.
func (c *AgentHTTP) SubscribeSSE(ctx context.Context, sessionID, after string, sequence uint64) tea.Cmd {
	return func() tea.Msg {
		events := make(chan agentEventMsg, 32)
		errCh := make(chan error, 1)
		go func() {
			defer close(events)
			url := fmt.Sprintf("%s/api/agent/sessions/%s/events", c.BaseURL, sessionID)
			if after != "" {
				url += "?after=" + after
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				errCh <- err
				return
			}
			req.Header.Set("Accept", "text/event-stream")
			if c.Token != "" {
				req.Header.Set("Authorization", "Bearer "+c.Token)
			}
			// Long-lived client without overall timeout.
			client := &http.Client{Timeout: 0}
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				errCh <- fmt.Errorf("sse HTTP %d: %s", resp.StatusCode, string(body))
				return
			}
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
			var data lines
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, ":") {
					continue
				}
				if line == "" {
					if data.text == "" {
						continue
					}
					var ev agentEventPayload
					if err := json.Unmarshal([]byte(data.text), &ev); err == nil {
						select {
						case events <- agentEventMsg{Sequence: sequence, SessionID: sessionID, Event: ev}:
						case <-ctx.Done():
							return
						}
					}
					data.text = ""
					continue
				}
				if strings.HasPrefix(line, "data:") {
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if data.text != "" {
						data.text += "\n"
					}
					data.text += payload
				}
			}
			if err := scanner.Err(); err != nil && ctx.Err() == nil {
				errCh <- err
			}
		}()
		// First wait: either an error before any event, or hand off to waitAgentEventCmd.
		select {
		case err := <-errCh:
			return agentErrMsg{Sequence: sequence, Err: err}
		case ev, ok := <-events:
			if !ok {
				select {
				case err := <-errCh:
					return agentErrMsg{Sequence: sequence, Err: err}
				default:
					return agentErrMsg{Sequence: sequence, Err: fmt.Errorf("agent event stream closed")}
				}
			}
			return agentStreamReadyMsg{Sequence: sequence, SessionID: sessionID, Events: events, Errs: errCh, First: ev}
		case <-ctx.Done():
			return agentErrMsg{Sequence: sequence, Err: ctx.Err()}
		}
	}
}

type lines struct{ text string }

func waitAgentEventCmd(sequence uint64, sessionID string, events <-chan agentEventMsg, errs <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case err := <-errs:
			if err != nil {
				return agentErrMsg{Sequence: sequence, Err: err}
			}
			return agentErrMsg{Sequence: sequence, Err: fmt.Errorf("agent event stream closed")}
		case ev, ok := <-events:
			if !ok {
				return agentErrMsg{Sequence: sequence, Err: fmt.Errorf("agent event stream closed")}
			}
			ev.Sequence = sequence
			return ev
		}
	}
}

func (c *AgentHTTP) getJSON(ctx context.Context, path string, dst any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, dst)
}

func (c *AgentHTTP) postJSON(ctx context.Context, path string, body any, dst any, mutating bool) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	_ = mutating
	return c.doJSON(req, dst)
}

func (c *AgentHTTP) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Membox-Agent-Client", c.ClientID)
	req.Header.Set("X-Membox-CSRF", "1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c *AgentHTTP) doJSON(req *http.Request, dst any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		if errBody.Error.Message != "" {
			return fmt.Errorf("%s", errBody.Error.Message)
		}
		return fmt.Errorf("agent HTTP %d: %s", resp.StatusCode, truncateRunes(string(raw), 200))
	}
	if dst == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ensureAgentClientCmd brings up Companion and returns a ready agent client.
func ensureAgentClientCmd(ctx context.Context, app App, clientID string, sequence uint64) tea.Cmd {
	return func() tea.Msg {
		view, err := app.EnsureWebCompanion(ctx, "")
		if err != nil {
			return agentReadyMsg{Sequence: sequence, Err: err}
		}
		if !view.Running || view.URL == "" {
			return agentReadyMsg{Sequence: sequence, Err: fmt.Errorf("web companion is not running")}
		}
		// Bridge token for non-browser clients.
		token := ""
		if provider, ok := app.(interface{ BridgeInfo() (string, string) }); ok {
			_, token = provider.BridgeInfo()
		}
		client := newAgentHTTP(view.URL, token, clientID)
		status, err := client.Status(ctx)
		if err != nil {
			return agentReadyMsg{Sequence: sequence, Err: err, Client: client}
		}
		if !status.Enabled {
			return agentReadyMsg{Sequence: sequence, Err: fmt.Errorf("agent is disabled"), Client: client, Status: status}
		}
		if !status.Available {
			msg := "agent unavailable"
			if status.Error != nil && *status.Error != "" {
				msg = *status.Error
			}
			return agentReadyMsg{Sequence: sequence, Err: fmt.Errorf("%s", msg), Client: client, Status: status}
		}
		return agentReadyMsg{Sequence: sequence, Client: client, Status: status}
	}
}

// Bridge-aware App extension implemented by *membox.Box.
var _ interface{ BridgeInfo() (string, string) } = (*membox.Box)(nil)
