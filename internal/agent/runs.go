package agent

import (
	"context"
	"strings"
	"time"
)

// idempotencyEntry remembers an accepted prompt so HTTP retries do not start
// a second model run.
type idempotencyEntry struct {
	runID     string
	expiresAt time.Time
}

// runState tracks controller ownership of one in-flight run.
type runState struct {
	ID           string
	SessionID    string
	ControllerID string
	DocumentID   string
	StartedAt    time.Time
	// controller lease retention after disconnect
	ControllerSeen time.Time
}

// Prompt submits one user turn. The run survives the calling HTTP request;
// only an explicit abort ends it early.
func (m *manager) Prompt(ctx context.Context, cmd PromptCommand) (RunView, error) {
	if strings.TrimSpace(cmd.Text) == "" {
		return RunView{}, fmtError(CodeInvalidRequest, "prompt text is required")
	}
	if strings.TrimSpace(cmd.ClientID) == "" {
		return RunView{}, fmtError(CodeInvalidRequest, "client id is required")
	}
	if err := m.ensureProbed(ctx); err != nil {
		return RunView{}, err
	}
	if cmd.IdempotencyKey != "" {
		key := idemKey(cmd.ClientID, cmd.SessionID, cmd.IdempotencyKey)
		m.mu.Lock()
		entry, ok := m.idempotency[key]
		m.mu.Unlock()
		if ok && time.Now().Before(entry.expiresAt) {
			return RunView{RunID: entry.runID, Accepted: true}, nil
		}
	}

	slot, err := m.loadSession(ctx, cmd.SessionID)
	if err != nil {
		return RunView{}, err
	}
	if slot.worker.IsBusy() {
		return RunView{}, fmtError(CodeBusy, "The session is already running.")
	}

	// Validate document context and stash it for the extension's
	// before_agent_start fetch.
	docID := strings.TrimSpace(cmd.DocumentID)
	if docID != "" {
		if m.cfg.Tools == nil {
			return RunView{}, fmtError(CodeInvalidRequest, "document tools unavailable")
		}
		if _, err := m.cfg.Tools.GetDocument(ctx, docID); err != nil {
			return RunView{}, fmtError(CodeInvalidRequest, "document context not found")
		}
		m.storeRunContext(cmd.SessionID, docID)
	}

	runID := newEntityID()
	// context.Background() by design: once accepted, the run belongs to the
	// worker, not the HTTP request. Abort is the explicit end-of-run path.
	if err := slot.worker.prompt(context.Background(), runID, cmd.ClientID, cmd.Text, docID); err != nil {
		return RunView{}, err
	}
	m.mu.Lock()
	m.runs[runID] = &runState{
		ID:             runID,
		SessionID:      cmd.SessionID,
		ControllerID:   cmd.ClientID,
		DocumentID:     docID,
		StartedAt:      time.Now().UTC(),
		ControllerSeen: time.Now(),
	}
	if cmd.IdempotencyKey != "" {
		m.idempotency[idemKey(cmd.ClientID, cmd.SessionID, cmd.IdempotencyKey)] = idempotencyEntry{
			runID:     runID,
			expiresAt: time.Now().Add(10 * time.Minute),
		}
		m.pruneIdempotencyLocked()
	}
	m.mu.Unlock()
	_ = m.cfg.Catalog.TouchSession(ctx, cmd.SessionID, time.Now().UTC())
	return RunView{RunID: runID, Accepted: true}, nil
}

// Abort ends the active run. Any authenticated client may abort; UIs must
// make clear they are aborting a shared run.
func (m *manager) Abort(ctx context.Context, sessionID, runID, _ string) error {
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	_, _, _, currentRun, _, _, _ := slot.worker.SnapshotInfo()
	if currentRun != "" && runID != "" && currentRun != runID {
		return fmtError(CodeInvalidRequest, "run is not active")
	}
	return slot.worker.abort(ctx)
}

// Claim transfers controller ownership after the previous controller's lease
// expired (30s without activity).
func (m *manager) Claim(ctx context.Context, sessionID, runID, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok || run.SessionID != sessionID {
		return fmtError(CodeInvalidRequest, "run not found")
	}
	if time.Since(run.ControllerSeen) < 30*time.Second && run.ControllerID != clientID {
		return fmtError(CodeNotRunController, "controller lease still active")
	}
	run.ControllerID = clientID
	run.ControllerSeen = time.Now()
	if slot, ok := m.workers[sessionID]; ok && slot.worker != nil {
		slot.worker.SetController(clientID)
	}
	return nil
}

func (m *manager) Subscribe(ctx context.Context, sessionID, afterEventID string) (Subscription, error) {
	if err := m.ensureProbed(ctx); err != nil {
		return nil, err
	}
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return slot.stream.Subscribe(ctx, afterEventID)
}

func (m *manager) pruneIdempotencyLocked() {
	now := time.Now()
	for k, v := range m.idempotency {
		if now.After(v.expiresAt) {
			delete(m.idempotency, k)
		}
	}
	if len(m.idempotency) > 1000 {
		for k := range m.idempotency {
			delete(m.idempotency, k)
			if len(m.idempotency) <= 500 {
				break
			}
		}
	}
}

func idemKey(clientID, sessionID, key string) string {
	return clientID + "|" + sessionID + "|" + key
}
