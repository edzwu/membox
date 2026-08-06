package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// workerConfig configures one Pi RPC subprocess.
type workerConfig struct {
	PiPath        string
	SessionDir    string
	SessionPath   string // empty = new session
	ExtensionPath string
	Tools         []string
	Home          string
	// Internal API for the extension.
	InternalBaseURL string
	InternalToken   string
	WorkerID        string
	SessionID       string // membox session id
	// Optional initial model/thinking.
	ModelProvider string
	ModelID       string
	ThinkingLevel string
	Title         string
	// WriteTools controls whether the embedded extension registers write tools.
	WriteTools bool
}

// worker is one Pi RPC process bound to a single membox session.
type worker struct {
	cfg workerConfig

	cmd    *exec.Cmd
	stdin  *jsonlWriter
	stdout io.ReadCloser
	stderr *ringLog

	mu       sync.Mutex
	pending  map[string]chan rpcResponse
	state    string
	piSID    string
	piFile   string
	model    *ModelRef
	thinking string
	runID    string
	clientID string
	docID    string
	started  time.Time
	lastIdle time.Time
	crashes  int

	stream *eventStream

	// runContext lives for the worker lifetime (not an HTTP request).
	ctx    context.Context
	cancel context.CancelFunc

	// events from stdout decoder
	rawEvents chan rpcEvent

	closed atomic.Bool
	wg     sync.WaitGroup

	// onEvent is invoked for every normalized public event (optional).
	onSettled func(runID string, interrupted bool)
}

func newWorker(parent context.Context, cfg workerConfig, stream *eventStream) *worker {
	ctx, cancel := context.WithCancel(parent)
	return &worker{
		cfg:       cfg,
		pending:   make(map[string]chan rpcResponse),
		state:     StateStarting,
		stream:    stream,
		ctx:       ctx,
		cancel:    cancel,
		rawEvents: make(chan rpcEvent, 256),
		stderr:    newRingLog(200, 64<<10),
		lastIdle:  time.Now(),
	}
}

func (w *worker) Start() error {
	args := []string{
		"--mode", "rpc",
		"--session-dir", w.cfg.SessionDir,
		"--no-builtin-tools",
		"--no-extensions",
		"--extension", w.cfg.ExtensionPath,
		"--no-skills",
		"--no-prompt-templates",
		"--no-context-files",
		"--tools", strings.Join(w.cfg.Tools, ","),
	}
	if w.cfg.SessionPath != "" {
		args = append(args, "--session", w.cfg.SessionPath)
	}
	if w.cfg.Title != "" {
		args = append(args, "--name", w.cfg.Title)
	}
	if w.cfg.ModelProvider != "" && w.cfg.ModelID != "" {
		args = append(args, "--model", w.cfg.ModelProvider+"/"+w.cfg.ModelID)
	}
	if w.cfg.ThinkingLevel != "" {
		args = append(args, "--thinking", w.cfg.ThinkingLevel)
	}

	cmd := exec.CommandContext(w.ctx, w.cfg.PiPath, args...)
	cmd.Dir = w.cfg.Home
	cmd.Env = append(os.Environ(),
		"MEMBOX_AGENT_BASE_URL="+w.cfg.InternalBaseURL,
		"MEMBOX_AGENT_TOKEN="+w.cfg.InternalToken,
		"MEMBOX_AGENT_WORKER_ID="+w.cfg.WorkerID,
		"MEMBOX_AGENT_SESSION_ID="+w.cfg.SessionID,
		"MEMBOX_AGENT_WRITE_TOOLS="+writeToolsFlag(w.cfg.WriteTools),
		// Reduce ambient noise.
		"PI_OFFLINE=1",
	)
	// Put the worker in its own process group so Companion can reap the tree.
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return wrapError(CodeWorkerStartFailed, "stdin pipe", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return wrapError(CodeWorkerStartFailed, "stdout pipe", err)
	}
	cmd.Stderr = w.stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return wrapError(CodeWorkerStartFailed, "start pi", err)
	}
	w.cmd = cmd
	w.stdin = newJSONLWriter(stdin)
	w.stdout = stdout

	w.wg.Add(2)
	go w.readStdout()
	go w.dispatchEvents()

	// Bootstrap: get_state to learn session file/id.
	state, err := w.getState(w.ctx)
	if err != nil {
		_ = w.Stop(context.Background())
		return wrapError(CodeWorkerStartFailed, "get_state after start", err)
	}
	w.mu.Lock()
	w.piSID = state.SessionID
	w.piFile = state.SessionFile
	w.thinking = state.ThinkingLevel
	if state.Model != nil {
		w.model = state.Model
	}
	w.state = StateIdle
	w.lastIdle = time.Now()
	w.mu.Unlock()
	return nil
}

type piState struct {
	SessionID     string
	SessionFile   string
	ThinkingLevel string
	IsStreaming   bool
	MessageCount  int
	Model         *ModelRef
}

func (w *worker) getState(ctx context.Context) (piState, error) {
	resp, err := w.call(ctx, rpcCommand{"type": "get_state"})
	if err != nil {
		return piState{}, err
	}
	if !resp.Success {
		return piState{}, fmtError(CodeWorkerStartFailed, "get_state failed: %s", resp.Error)
	}
	var data struct {
		SessionID     string          `json:"sessionId"`
		SessionFile   string          `json:"sessionFile"`
		ThinkingLevel string          `json:"thinkingLevel"`
		IsStreaming   bool            `json:"isStreaming"`
		MessageCount  int             `json:"messageCount"`
		Model         json.RawMessage `json:"model"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return piState{}, wrapError(CodeProtocolError, "decode get_state", err)
	}
	out := piState{
		SessionID:     data.SessionID,
		SessionFile:   data.SessionFile,
		ThinkingLevel: data.ThinkingLevel,
		IsStreaming:   data.IsStreaming,
		MessageCount:  data.MessageCount,
	}
	if len(data.Model) > 0 && string(data.Model) != "null" {
		var m struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Name     string `json:"name"`
		}
		if err := json.Unmarshal(data.Model, &m); err == nil && m.ID != "" {
			out.Model = &ModelRef{Provider: m.Provider, ID: m.ID, Name: m.Name}
		}
	}
	return out, nil
}

func (w *worker) getMessages(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := w.call(ctx, rpcCommand{"type": "get_messages"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmtError(CodeProtocolError, "get_messages failed: %s", resp.Error)
	}
	var data struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, wrapError(CodeProtocolError, "decode get_messages", err)
	}
	return data.Messages, nil
}

func (w *worker) getAvailableModels(ctx context.Context) ([]ModelRef, error) {
	resp, err := w.call(ctx, rpcCommand{"type": "get_available_models"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmtError(CodeProtocolError, "get_available_models failed: %s", resp.Error)
	}
	var data struct {
		Models []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, wrapError(CodeProtocolError, "decode models", err)
	}
	out := make([]ModelRef, 0, len(data.Models))
	for _, m := range data.Models {
		out = append(out, ModelRef{Provider: m.Provider, ID: m.ID, Name: m.Name})
	}
	return out, nil
}

func (w *worker) setModel(ctx context.Context, model ModelRef) error {
	resp, err := w.call(ctx, rpcCommand{
		"type":     "set_model",
		"provider": model.Provider,
		"modelId":  model.ID,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmtError(CodeInvalidRequest, "set_model failed: %s", resp.Error)
	}
	w.mu.Lock()
	w.model = &ModelRef{Provider: model.Provider, ID: model.ID, Name: model.Name}
	w.mu.Unlock()
	w.stream.Publish(Event{
		SessionID: w.cfg.SessionID,
		Type:      EventModelChanged,
		Payload:   payloadObject(map[string]any{"model": w.model}),
	})
	return nil
}

func (w *worker) setThinking(ctx context.Context, level string) error {
	resp, err := w.call(ctx, rpcCommand{"type": "set_thinking_level", "level": level})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmtError(CodeInvalidRequest, "set_thinking_level failed: %s", resp.Error)
	}
	w.mu.Lock()
	w.thinking = level
	w.mu.Unlock()
	return nil
}

func (w *worker) prompt(ctx context.Context, runID, clientID, text, documentID string) error {
	w.mu.Lock()
	if w.state == StateRunning || w.state == StateWaitingApproval {
		w.mu.Unlock()
		return fmtError(CodeBusy, "The session is already running.")
	}
	w.state = StateRunning
	w.runID = runID
	w.clientID = clientID
	w.docID = documentID
	w.started = time.Now().UTC()
	w.mu.Unlock()

	w.stream.Publish(Event{
		SessionID: w.cfg.SessionID,
		RunID:     runID,
		Type:      EventRunStarted,
		Payload: payloadObject(map[string]any{
			"client_id":   clientID,
			"document_id": documentID,
		}),
	})
	w.stream.Publish(Event{
		SessionID: w.cfg.SessionID,
		RunID:     runID,
		Type:      EventUserAccepted,
		Payload:   payloadObject(map[string]any{"text": text}),
	})

	resp, err := w.call(ctx, rpcCommand{"type": "prompt", "message": text})
	if err != nil {
		w.markInterrupted(runID)
		return err
	}
	if !resp.Success {
		w.markInterrupted(runID)
		return fmtError(CodeInvalidRequest, "prompt rejected: %s", resp.Error)
	}
	return nil
}

func (w *worker) abort(ctx context.Context) error {
	resp, err := w.call(ctx, rpcCommand{"type": "abort"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmtError(CodeInvalidRequest, "abort failed: %s", resp.Error)
	}
	return nil
}

func (w *worker) respondExtensionUI(ctx context.Context, id string, body map[string]any) error {
	cmd := rpcCommand{"type": "extension_ui_response", "id": id}
	for k, v := range body {
		cmd[k] = v
	}
	// Fire-and-forget style: Pi does not emit a typed response for UI replies,
	// but we still write through the serial stdin writer.
	_ = ctx
	return w.stdin.WriteJSON(cmd)
}

func (w *worker) call(ctx context.Context, cmd rpcCommand) (rpcResponse, error) {
	if w.closed.Load() {
		return rpcResponse{}, fmtError(CodeWorkerExited, "worker is not running")
	}
	id := newCommandID()
	cmd["id"] = id
	ch := make(chan rpcResponse, 1)
	w.mu.Lock()
	w.pending[id] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
	}()

	if err := w.stdin.WriteJSON(cmd); err != nil {
		return rpcResponse{}, err
	}
	select {
	case <-ctx.Done():
		return rpcResponse{}, ctx.Err()
	case <-w.ctx.Done():
		return rpcResponse{}, fmtError(CodeWorkerExited, "worker context canceled")
	case resp := <-ch:
		return resp, nil
	}
}

func (w *worker) readStdout() {
	defer w.wg.Done()
	reader := newJSONLReader(w.stdout, DefaultMaxFrameBytes)
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if err != io.EOF && !w.closed.Load() {
				w.stream.Publish(Event{
					SessionID: w.cfg.SessionID,
					Type:      EventAgentError,
					Payload:   payloadObject(map[string]any{"code": CodeProtocolError, "message": err.Error()}),
				})
			}
			w.failPending(fmtError(CodeWorkerExited, "worker stdout closed"))
			close(w.rawEvents)
			return
		}
		event, err := decodeRPCEvent(frame)
		if err != nil {
			w.stream.Publish(Event{
				SessionID: w.cfg.SessionID,
				Type:      EventAgentError,
				Payload:   payloadObject(map[string]any{"code": CodeProtocolError, "message": err.Error()}),
			})
			// Protocol errors are fatal for the worker.
			w.cancel()
			w.failPending(err)
			close(w.rawEvents)
			return
		}
		if event.Type() == "response" {
			var resp rpcResponse
			raw, _ := json.Marshal(event)
			if err := json.Unmarshal(raw, &resp); err != nil {
				continue
			}
			w.mu.Lock()
			ch := w.pending[resp.ID]
			w.mu.Unlock()
			if ch != nil {
				select {
				case ch <- resp:
				default:
				}
			}
			continue
		}
		select {
		case w.rawEvents <- event:
		case <-w.ctx.Done():
			w.failPending(fmtError(CodeWorkerExited, "worker canceled"))
			close(w.rawEvents)
			return
		}
	}
}

func (w *worker) dispatchEvents() {
	defer w.wg.Done()
	var (
		assistantStarted bool
		currentRun       string
	)
	for event := range w.rawEvents {
		w.mu.Lock()
		runID := w.runID
		sessionID := w.cfg.SessionID
		w.mu.Unlock()
		if currentRun == "" {
			currentRun = runID
		}

		switch event.Type() {
		case "agent_start":
			assistantStarted = false
			w.stream.Publish(Event{SessionID: sessionID, RunID: runID, Type: EventSessionState, Payload: payloadObject(map[string]any{"state": StateRunning})})

		case "message_update":
			var envelope struct {
				AssistantMessageEvent struct {
					Type  string `json:"type"`
					Delta string `json:"delta"`
				} `json:"assistantMessageEvent"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			raw, _ := json.Marshal(event)
			_ = json.Unmarshal(raw, &envelope)
			ame := envelope.AssistantMessageEvent
			switch ame.Type {
			case "text_start":
				if !assistantStarted {
					assistantStarted = true
					w.stream.Publish(Event{SessionID: sessionID, RunID: runID, Type: EventAssistantStarted, Payload: payloadObject(map[string]any{})})
				}
			case "text_delta":
				if ame.Delta == "" {
					continue
				}
				if !assistantStarted {
					assistantStarted = true
					w.stream.Publish(Event{SessionID: sessionID, RunID: runID, Type: EventAssistantStarted, Payload: payloadObject(map[string]any{})})
				}
				w.stream.Publish(Event{
					SessionID: sessionID,
					RunID:     runID,
					Type:      EventAssistantDelta,
					Payload:   payloadObject(map[string]any{"text": ame.Delta}),
				})
			case "text_end":
				// completion handled on message_end / agent_settled
			case "thinking_start", "thinking_delta", "thinking_end":
				// Intentionally not forwarded; UI shows generic Thinking… via session.state.
			}

		case "message_end":
			var msg struct {
				Message struct {
					Role    string `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			raw, _ := json.Marshal(event)
			_ = json.Unmarshal(raw, &msg)
			if msg.Message.Role == "assistant" && assistantStarted {
				w.stream.Publish(Event{
					SessionID: sessionID,
					RunID:     runID,
					Type:      EventAssistantCompleted,
					Payload:   payloadObject(map[string]any{}),
				})
				assistantStarted = false
			}

		case "tool_execution_start":
			var body struct {
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Args       json.RawMessage `json:"args"`
			}
			raw, _ := json.Marshal(event)
			_ = json.Unmarshal(raw, &body)
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     runID,
				Type:      EventToolStarted,
				Payload: payloadObject(map[string]any{
					"tool_call_id": body.ToolCallID,
					"tool":         sanitizeToolName(body.ToolName),
					"args":         sanitizeToolArgs(body.Args),
				}),
			})

		case "tool_execution_update":
			var body struct {
				ToolCallID string `json:"toolCallId"`
				ToolName   string `json:"toolName"`
			}
			raw, _ := json.Marshal(event)
			_ = json.Unmarshal(raw, &body)
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     runID,
				Type:      EventToolProgress,
				Payload: payloadObject(map[string]any{
					"tool_call_id": body.ToolCallID,
					"tool":         sanitizeToolName(body.ToolName),
				}),
			})

		case "tool_execution_end":
			var body struct {
				ToolCallID string `json:"toolCallId"`
				ToolName   string `json:"toolName"`
				IsError    bool   `json:"isError"`
				Result     json.RawMessage `json:"result"`
			}
			raw, _ := json.Marshal(event)
			_ = json.Unmarshal(raw, &body)
			status := "success"
			if body.IsError {
				status = "error"
			}
			// Detect denied write results from our tools.
			if bytesContains(body.Result, []byte(`"denied":true`)) || bytesContains(body.Result, []byte(`"denied": true`)) {
				status = "denied"
			}
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     runID,
				Type:      EventToolCompleted,
				Payload: payloadObject(map[string]any{
					"tool_call_id": body.ToolCallID,
					"tool":         sanitizeToolName(body.ToolName),
					"status":       status,
					"summary":      truncate(string(body.Result), 400),
				}),
			})

		case "extension_ui_request":
			w.handleExtensionUI(event, runID)

		case "agent_settled":
			w.mu.Lock()
			settledRun := w.runID
			w.state = StateIdle
			w.runID = ""
			w.clientID = ""
			w.docID = ""
			w.lastIdle = time.Now()
			cb := w.onSettled
			w.mu.Unlock()
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     settledRun,
				Type:      EventRunSettled,
				Payload:   payloadObject(map[string]any{}),
			})
			w.stream.Publish(Event{
				SessionID: sessionID,
				Type:      EventSessionState,
				Payload:   payloadObject(map[string]any{"state": StateIdle}),
			})
			if cb != nil {
				cb(settledRun, false)
			}
			currentRun = ""
			assistantStarted = false

		case "agent_end":
			// Do not end the run here; wait for agent_settled.
		case "extension_error":
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     runID,
				Type:      EventAgentError,
				Payload: payloadObject(map[string]any{
					"code":    "extension_error",
					"message": event.String("error"),
				}),
			})
		case "queue_update":
			w.stream.Publish(Event{
				SessionID: sessionID,
				RunID:     runID,
				Type:      EventQueueUpdated,
				Payload:   payloadObject(map[string]any{}),
			})
		}
	}
}

func (w *worker) handleExtensionUI(event rpcEvent, runID string) {
	var req struct {
		ID      string `json:"id"`
		Method  string `json:"method"`
		Title   string `json:"title"`
		Message string `json:"message"`
		Timeout int    `json:"timeout"`
	}
	raw, _ := json.Marshal(event)
	_ = json.Unmarshal(raw, &req)
	switch req.Method {
	case "confirm":
		approvalID := req.ID
		if approvalID == "" {
			approvalID = newCommandID()
		}
		w.mu.Lock()
		w.state = StateWaitingApproval
		controller := w.clientID
		w.mu.Unlock()
		w.stream.Publish(Event{
			SessionID: w.cfg.SessionID,
			RunID:     runID,
			Type:      EventApprovalRequested,
			Payload: payloadObject(map[string]any{
				"approval_id":   approvalID,
				"title":         req.Title,
				"message":       req.Message,
				"controller_id": controller,
				"timeout_ms":    req.Timeout,
			}),
		})
		// Actual resolution is delivered via Manager.ResolveApproval → respondExtensionUI.
	case "notify", "setStatus", "setWidget", "setTitle", "set_editor_text":
		// Fire-and-forget; ignore for V1 UI.
	default:
		// Auto-cancel unsupported dialogs so the agent is not stuck.
		if req.ID != "" {
			_ = w.respondExtensionUI(w.ctx, req.ID, map[string]any{"cancelled": true})
		}
	}
}

func (w *worker) markInterrupted(runID string) {
	w.mu.Lock()
	w.state = StateIdle
	w.runID = ""
	w.clientID = ""
	w.docID = ""
	w.lastIdle = time.Now()
	cb := w.onSettled
	w.mu.Unlock()
	w.stream.Publish(Event{
		SessionID: w.cfg.SessionID,
		RunID:     runID,
		Type:      EventRunInterrupted,
		Payload:   payloadObject(map[string]any{"reason": "error"}),
	})
	if cb != nil {
		cb(runID, true)
	}
}

func (w *worker) failPending(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, ch := range w.pending {
		select {
		case ch <- rpcResponse{ID: id, Type: "response", Success: false, Error: err.Error()}:
		default:
		}
		delete(w.pending, id)
	}
	if w.state == StateRunning || w.state == StateWaitingApproval {
		runID := w.runID
		w.state = StateFailed
		w.runID = ""
		go func() {
			w.stream.Publish(Event{
				SessionID: w.cfg.SessionID,
				RunID:     runID,
				Type:      EventRunInterrupted,
				Payload:   payloadObject(map[string]any{"reason": "worker_exited"}),
			})
		}()
	}
}

func (w *worker) SnapshotInfo() (state string, model *ModelRef, thinking, runID, clientID, docID string, started time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, cloneModel(w.model), w.thinking, w.runID, w.clientID, w.docID, w.started
}

func (w *worker) PiSession() (id, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.piSID, w.piFile
}

func (w *worker) IsIdle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state == StateIdle
}

func (w *worker) IdleSince() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastIdle
}

func (w *worker) Stop(ctx context.Context) error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Best-effort abort of current run.
	if w.stdin != nil && w.IsBusy() {
		abortCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = w.abort(abortCtx)
		cancel()
	}
	w.cancel()
	if w.cmd != nil && w.cmd.Process != nil {
		// Wait briefly for graceful exit after stdin/context cancel.
		done := make(chan error, 1)
		go func() { done <- w.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			killProcessGroup(w.cmd)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		case <-ctx.Done():
			killProcessGroup(w.cmd)
		}
	}
	w.wg.Wait()
	return nil
}

func (w *worker) IsBusy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state == StateRunning || w.state == StateWaitingApproval
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}

func newCommandID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id.String()
}

func newEntityID() string {
	return newCommandID()
}

func cloneModel(m *ModelRef) *ModelRef {
	if m == nil {
		return nil
	}
	cp := *m
	return &cp
}

func sanitizeToolName(name string) string {
	return strings.TrimSpace(name)
}

func sanitizeToolArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage(`{}`)
	}
	// Drop fields that might leak secrets if a tool ever includes them.
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil {
		return json.RawMessage(`{}`)
	}
	for _, key := range []string{"token", "authorization", "password", "api_key", "bearer"} {
		delete(obj, key)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	if len(raw) > 2000 {
		return json.RawMessage(`{"truncated":true}`)
	}
	return raw
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func bytesContains(b, sub []byte) bool {
	return strings.Contains(string(b), string(sub))
}

// materializeExtension writes the embedded extension to a stable path under home.
func materializeExtension(home string, source []byte) (string, error) {
	dir := filepath.Join(home, "agent", "extensions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "membox.ts")
	tmp, err := os.CreateTemp(dir, "membox-*.ts.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(source); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return target, nil
}
