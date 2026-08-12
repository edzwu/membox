package membox

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

	"membox/internal/web/companion"
)

// summarizeAgentClient is the X-Membox-Agent-Client header value used for
// one-shot summarize runs (CLI `doc summarize`, TUI `s` key).
const summarizeAgentClient = "membox-summarize"

// summarizeMaxBodyBytes caps the document content embedded in the prompt so a
// huge clip cannot blow up the agent request.
const summarizeMaxBodyBytes = 120 * 1024

// summarizePrompt instructs the agent; the document title and body are
// appended after it.
const summarizePrompt = "请阅读以下 Markdown 文档，输出一份要点式总结（中文，分点列出核心概念，最后给一行核心模式/结论）。只输出总结本身，不要解释你在做什么。\n\n文档标题："

// SummarizeDocument asks the companion-hosted agent to summarize the document
// and persists the result as the document's summary. It creates a throwaway
// session, posts one prompt containing the document body, and consumes the
// SSE stream until the run settles. Use SetDocumentSummary to store an
// explicit text instead.
func (b *Box) SummarizeDocument(ctx context.Context, selector string) (DocumentView, error) {
	summary, err := b.agentSummarize(ctx, selector)
	if err != nil {
		return DocumentView{}, err
	}
	if strings.TrimSpace(summary) == "" {
		return DocumentView{}, fmt.Errorf("agent returned an empty summary")
	}
	resolved, err := b.ResolveDocumentLocation(ctx, ResolveLocationQuery{Selector: selector})
	if err != nil {
		return DocumentView{}, err
	}
	return b.SetDocumentSummary(ctx, SetSummaryCommand{Selector: resolved.DocumentID, Summary: summary})
}

// agentSummarize runs the one-shot agent turn and returns the assistant's
// reply text.
func (b *Box) agentSummarize(ctx context.Context, selector string) (string, error) {
	status, err := b.WebStatus(ctx)
	if err != nil {
		return "", err
	}
	if !status.Running {
		if _, err := b.EnsureWebCompanion(ctx, companion.LifecycleKeep); err != nil {
			return "", fmt.Errorf("starting web companion for the agent: %w", err)
		}
		if status, err = b.WebStatus(ctx); err != nil {
			return "", err
		}
		if !status.Running {
			return "", fmt.Errorf("web companion is not running; the agent needs it (try `mm web start`)")
		}
	}
	baseURL, token := b.BridgeInfo()

	document, err := b.GetDocument(ctx, GetDocumentQuery{Selector: selector})
	if err != nil {
		return "", err
	}
	if document.Status != "active" {
		return "", fmt.Errorf("document %s is %s at %s", document.ID, document.Status, document.Path)
	}
	body, err := b.ReadDocument(ctx, ReadDocumentQuery{Selector: document.ID})
	if err != nil {
		return "", err
	}
	if len(body) > summarizeMaxBodyBytes {
		body = body[:summarizeMaxBodyBytes]
	}

	client := &http.Client{}

	// 1. Throwaway session.
	var session struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := companionAgentPost(ctx, client, baseURL, token, "/api/agent/sessions",
		map[string]any{"title": "summarize " + shortDocumentID(document.ID)}, &session); err != nil {
		return "", fmt.Errorf("creating agent session: %w", err)
	}
	if session.Session.ID == "" {
		return "", fmt.Errorf("agent session response missing session id")
	}
	sessionID := session.Session.ID

	// 2. Subscribe to events before prompting so no delta is lost.
	events, eventErrs, cancelEvents, err := subscribeAgentEvents(ctx, client, baseURL, token, sessionID)
	if err != nil {
		return "", fmt.Errorf("subscribing to agent events: %w", err)
	}
	defer cancelEvents()

	// 3. One prompt turn with the full body inline: the agent needs no tools.
	prompt := summarizePrompt + document.Title + "\n\n" + string(body)
	var runAck struct {
		RunID    string `json:"run_id"`
		Accepted bool   `json:"accepted"`
	}
	if err := companionAgentPost(ctx, client, baseURL, token, "/api/agent/sessions/"+sessionID+"/messages",
		map[string]any{
			"text":    prompt,
			"context": map[string]any{"document_id": document.ID},
		}, &runAck); err != nil {
		return "", fmt.Errorf("prompting agent: %w", err)
	}
	if !runAck.Accepted {
		return "", fmt.Errorf("agent did not accept the run")
	}

	// 4. Accumulate assistant deltas until the run settles.
	var text strings.Builder
	timeout := time.NewTimer(3 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", fmt.Errorf("agent run timed out after 3 minutes")
		case err := <-eventErrs:
			if err != nil {
				return "", err
			}
		case ev, ok := <-events:
			if !ok {
				return "", fmt.Errorf("agent event stream closed before the run settled")
			}
			if runAck.RunID != "" && ev.RunID != "" && ev.RunID != runAck.RunID {
				continue
			}
			switch ev.Type {
			case "assistant.delta":
				var payload struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(ev.Payload, &payload) == nil {
					text.WriteString(payload.Text)
				}
			case "run.settled":
				return strings.TrimSpace(text.String()), nil
			case "run.interrupted":
				return "", fmt.Errorf("agent run was interrupted")
			case "agent.error":
				return "", fmt.Errorf("agent error: %s", strings.TrimSpace(string(ev.Payload)))
			}
		}
	}
}

// agentEvent is the wire shape published on the session SSE stream.
type agentEvent struct {
	ID      string          `json:"id"`
	RunID   string          `json:"run_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// subscribeAgentEvents opens the session SSE stream and demultiplexes parsed
// events onto a channel. The returned cancel must always be called.
func subscribeAgentEvents(ctx context.Context, client *http.Client, baseURL, token, sessionID string) (<-chan agentEvent, <-chan error, context.CancelFunc, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/api/agent/sessions/"+sessionID+"/events", nil)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		return nil, nil, nil, fmt.Errorf("agent events HTTP %d: %s", resp.StatusCode, truncateAgentBody(body, 200))
	}

	events := make(chan agentEvent, 64)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue // id:, event:, heartbeat comments
			}
			var ev agentEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				continue
			}
			select {
			case events <- ev:
			case <-streamCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && streamCtx.Err() == nil {
			errs <- fmt.Errorf("reading agent event stream: %w", err)
		}
	}()
	return events, errs, cancel, nil
}

// companionAgentPost sends one authenticated JSON POST to the companion and
// decodes the response body into dst (nil dst ignores the body).
func companionAgentPost(ctx context.Context, client *http.Client, baseURL, token, path string, payload any, dst any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Membox-Agent-Client", summarizeAgentClient)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateAgentBody(body, 300))
	}
	if dst != nil && len(body) > 0 {
		return json.Unmarshal(body, dst)
	}
	return nil
}

func shortDocumentID(id string) string {
	// Match the UI's short-ID convention: the UUID tail.
	if len(id) > 4 {
		return id[len(id)-4:]
	}
	return id
}

func truncateAgentBody(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
