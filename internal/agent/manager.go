package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Minimum Pi version accepted by the capability probe.
// Locked at implementation time; bump after contract tests pass on newer Pi.
const MinPiVersion = "0.1.0"

// Config configures the AgentManager.
type Config struct {
	Home           string
	Enabled        bool
	PiPath         string // empty = PATH lookup
	MaxWorkers     int
	IdleTimeout    time.Duration
	WriteTools     bool // V1 default false
	InternalBaseURL func() string
	// ExtensionSource is the TypeScript source materialized at startup.
	ExtensionSource []byte
	Catalog         SessionCatalog
	Tools           DocumentTools
	// Settings lookup (optional).
	GetSetting func(ctx context.Context, key string) (string, error)
}

// manager is the concrete AgentManager.
type manager struct {
	cfg Config

	mu            sync.Mutex
	state         string // disabled|probing|ready|unavailable
	piPath        string
	piVersion     string
	errReason     string
	extensionPath string
	sessionRoot   string
	workers       map[string]*sessionSlot // membox session id -> slot
	// idempotency: client|session|key -> runID
	idempotency map[string]idempotencyEntry
	// pending run contexts for approval routing
	approvals map[string]*pendingApproval // approvalID -> pending
	// run controllers
	runs map[string]*runState // runID -> state

	probedAt time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type sessionSlot struct {
	record   SessionRecord
	worker   *worker
	stream   *eventStream
	loading  bool
	failed   string
	lastUsed time.Time
}

type idempotencyEntry struct {
	runID     string
	expiresAt time.Time
}

type pendingApproval struct {
	SessionID    string
	RunID        string
	ControllerID string
	Worker       *worker
	PiRequestID  string
	ExpiresAt    time.Time
	Resolved     bool
	Decision     string
}

type runState struct {
	ID           string
	SessionID    string
	ControllerID string
	DocumentID   string
	StartedAt    time.Time
	// controller lease retention after disconnect
	ControllerSeen time.Time
}

// NewManager constructs a lazy-start AgentManager.
func NewManager(cfg Config) Manager {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 2
	}
	if cfg.MaxWorkers > 8 {
		cfg.MaxWorkers = 8 // hard cap
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 10 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &manager{
		cfg:         cfg,
		state:       StateDisabled,
		workers:     make(map[string]*sessionSlot),
		idempotency: make(map[string]idempotencyEntry),
		approvals:   make(map[string]*pendingApproval),
		runs:        make(map[string]*runState),
		ctx:         ctx,
		cancel:      cancel,
	}
	if cfg.Enabled {
		m.state = StateReady // probe on first Status
	}
	m.sessionRoot = filepath.Join(cfg.Home, "agent", "sessions")
	m.wg.Add(1)
	go m.evictionLoop()
	return m
}

func (m *manager) Status(ctx context.Context) (Status, error) {
	if !m.cfg.Enabled {
		return Status{
			Enabled:    false,
			Available:  false,
			State:      StateDisabled,
			MaxWorkers: m.cfg.MaxWorkers,
			WriteTools: false,
		}, nil
	}
	if err := m.ensureProbed(ctx); err != nil {
		reason := err.Error()
		return Status{
			Enabled:    true,
			Available:  false,
			State:      StateUnavailable,
			MaxWorkers: m.cfg.MaxWorkers,
			WriteTools: m.cfg.WriteTools,
			Error:      &reason,
			PiPath:     m.piPath,
			PiVersion:  m.piVersion,
		}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	loaded := 0
	for _, slot := range m.workers {
		if slot.worker != nil {
			loaded++
		}
	}
	return Status{
		Enabled:       true,
		Available:     m.state == StateReady,
		PiPath:        m.piPath,
		PiVersion:     m.piVersion,
		State:         m.state,
		LoadedWorkers: loaded,
		MaxWorkers:    m.cfg.MaxWorkers,
		WriteTools:    m.cfg.WriteTools,
		Error:         nilString(m.errReason),
	}, nil
}

func (m *manager) ensureProbed(ctx context.Context) error {
	m.mu.Lock()
	if m.state == StateReady && m.piPath != "" && time.Since(m.probedAt) < 5*time.Minute {
		m.mu.Unlock()
		return nil
	}
	m.state = StateProbing
	m.mu.Unlock()

	piPath := m.cfg.PiPath
	if m.cfg.GetSetting != nil {
		if setting, err := m.cfg.GetSetting(ctx, "agent.pi_path"); err == nil && strings.TrimSpace(setting) != "" {
			piPath = strings.TrimSpace(setting)
		}
	}
	resolved, version, err := probePi(ctx, piPath)
	if err != nil {
		m.mu.Lock()
		m.state = StateUnavailable
		m.errReason = err.Error()
		m.mu.Unlock()
		return err
	}
	if err := os.MkdirAll(m.sessionRoot, 0o700); err != nil {
		m.mu.Lock()
		m.state = StateUnavailable
		m.errReason = err.Error()
		m.mu.Unlock()
		return wrapError(CodeUnavailable, "session dir", err)
	}
	extPath := ""
	if len(m.cfg.ExtensionSource) > 0 {
		extPath, err = materializeExtension(m.cfg.Home, m.cfg.ExtensionSource)
		if err != nil {
			m.mu.Lock()
			m.state = StateUnavailable
			m.errReason = err.Error()
			m.mu.Unlock()
			return wrapError(CodeUnavailable, "materialize extension", err)
		}
	}
	m.mu.Lock()
	m.piPath = resolved
	m.piVersion = version
	m.extensionPath = extPath
	m.state = StateReady
	m.errReason = ""
	m.probedAt = time.Now()
	m.mu.Unlock()
	return nil
}

func probePi(ctx context.Context, configured string) (path, version string, err error) {
	candidates := []string{}
	if strings.TrimSpace(configured) != "" {
		candidates = append(candidates, configured)
	}
	if looked, lookErr := exec.LookPath("pi"); lookErr == nil {
		candidates = append(candidates, looked)
	}
	if len(candidates) == 0 {
		return "", "", fmtError(CodeUnavailable, "pi executable not found on PATH")
	}
	var last error
	for _, candidate := range candidates {
		cmd := exec.CommandContext(ctx, candidate, "--version")
		out, runErr := cmd.Output()
		if runErr != nil {
			last = runErr
			continue
		}
		ver := strings.TrimSpace(string(out))
		// Accept any non-empty version for now; MinPiVersion is advisory.
		if ver == "" {
			last = fmtError(CodeIncompatiblePi, "empty pi version")
			continue
		}
		return candidate, ver, nil
	}
	if last == nil {
		last = fmtError(CodeUnavailable, "pi probe failed")
	}
	return "", "", wrapError(CodeUnavailable, "pi probe failed", last)
}

func (m *manager) ListSessions(ctx context.Context, q ListSessionsQuery) ([]SessionView, error) {
	if m.cfg.Catalog == nil {
		return nil, fmtError(CodeInternal, "session catalog not configured")
	}
	recs, err := m.cfg.Catalog.ListSessions(ctx, q.IncludeArchived)
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(recs))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		state := StateUnloaded
		unavailable := false
		if slot, ok := m.workers[rec.ID]; ok && slot.worker != nil {
			state, _, _, _, _, _, _ = slot.worker.SnapshotInfo()
		} else if _, statErr := os.Stat(rec.SessionPath); statErr != nil {
			unavailable = true
			state = StateFailed
		}
		out = append(out, recordToView(rec, state, unavailable))
	}
	return out, nil
}

func (m *manager) CreateSession(ctx context.Context, cmd CreateSessionCommand) (SessionSnapshot, error) {
	if err := m.ensureProbed(ctx); err != nil {
		return SessionSnapshot{}, err
	}
	if m.cfg.Catalog == nil {
		return SessionSnapshot{}, fmtError(CodeInternal, "session catalog not configured")
	}
	// Idempotency for create: reuse key space with session="new"
	if cmd.IdempotencyKey != "" && cmd.ClientID != "" {
		if snap, ok := m.lookupCreateIdempotency(cmd.ClientID, cmd.IdempotencyKey); ok {
			return snap, nil
		}
	}
	sessionID := newEntityID()
	title := strings.TrimSpace(cmd.Title)
	if title == "" {
		title = "New chat"
	}
	thinking := cmd.ThinkingLevel
	if thinking == "" && m.cfg.GetSetting != nil {
		if v, _ := m.cfg.GetSetting(ctx, "agent.thinking_level"); v != "" {
			thinking = v
		}
	}
	if thinking == "" {
		thinking = "medium"
	}

	if err := m.ensureCapacity(ctx); err != nil {
		return SessionSnapshot{}, err
	}

	stream := newEventStream(sessionID)
	slot := &sessionSlot{stream: stream, lastUsed: time.Now()}
	worker, err := m.spawnWorker(ctx, sessionID, SessionRecord{
		ID:            sessionID,
		Title:         title,
		ThinkingLevel: thinking,
	}, "", stream, cmd.Model)
	if err != nil {
		return SessionSnapshot{}, err
	}
	piSID, piFile := worker.PiSession()
	absFile, err := validateSessionPath(m.sessionRoot, piFile)
	if err != nil {
		_ = worker.Stop(ctx)
		return SessionSnapshot{}, err
	}
	now := time.Now().UTC()
	rec := SessionRecord{
		ID:            sessionID,
		PiSessionID:   piSID,
		SessionPath:   absFile,
		Title:         title,
		ThinkingLevel: thinking,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastUsedAt:    now,
	}
	state, model, thinkingLevel, _, _, _, _ := worker.SnapshotInfo()
	if model != nil {
		rec.ModelProvider = model.Provider
		rec.ModelID = model.ID
	}
	if thinkingLevel != "" {
		rec.ThinkingLevel = thinkingLevel
	}
	if err := m.cfg.Catalog.InsertSession(ctx, rec); err != nil {
		_ = worker.Stop(ctx)
		return SessionSnapshot{}, err
	}
	slot.record = rec
	slot.worker = worker
	m.mu.Lock()
	m.workers[sessionID] = slot
	m.mu.Unlock()

	messages, _ := worker.getMessages(ctx)
	snap := SessionSnapshot{
		Session:     recordToView(rec, state, false),
		Messages:    messages,
		ActiveRun:   nil,
		LastEventID: stream.LastEventID(),
		StreamEpoch: stream.Epoch(),
	}
	if cmd.IdempotencyKey != "" && cmd.ClientID != "" {
		m.storeCreateIdempotency(cmd.ClientID, cmd.IdempotencyKey, snap)
	}
	return snap, nil
}

func (m *manager) Snapshot(ctx context.Context, sessionID string) (SessionSnapshot, error) {
	if err := m.ensureProbed(ctx); err != nil {
		return SessionSnapshot{}, err
	}
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	state, model, thinking, runID, clientID, docID, started := slot.worker.SnapshotInfo()
	messages, err := slot.worker.getMessages(ctx)
	if err != nil {
		messages = nil
	}
	view := recordToView(slot.record, state, false)
	view.Model = model
	view.ThinkingLevel = thinking
	var active *ActiveRun
	if runID != "" {
		active = &ActiveRun{
			ID:           runID,
			ControllerID: clientID,
			StartedAt:    started,
			DocumentID:   docID,
			State:        state,
		}
	}
	return SessionSnapshot{
		Session:     view,
		Messages:    messages,
		ActiveRun:   active,
		LastEventID: slot.stream.LastEventID(),
		StreamEpoch: slot.stream.Epoch(),
	}, nil
}

func (m *manager) ArchiveSession(ctx context.Context, sessionID string) error {
	if m.cfg.Catalog == nil {
		return fmtError(CodeInternal, "session catalog not configured")
	}
	m.mu.Lock()
	if slot, ok := m.workers[sessionID]; ok {
		w := slot.worker
		delete(m.workers, sessionID)
		m.mu.Unlock()
		if w != nil {
			_ = w.Stop(ctx)
		}
		if slot.stream != nil {
			slot.stream.Close()
		}
	} else {
		m.mu.Unlock()
	}
	return m.cfg.Catalog.ArchiveSession(ctx, sessionID, time.Now().UTC())
}

func (m *manager) RenameSession(ctx context.Context, sessionID, title string) (SessionView, error) {
	if m.cfg.Catalog == nil {
		return SessionView{}, fmtError(CodeInternal, "session catalog not configured")
	}
	rec, ok, err := m.cfg.Catalog.GetSession(ctx, sessionID)
	if err != nil {
		return SessionView{}, err
	}
	if !ok {
		return SessionView{}, fmtError(CodeSessionNotFound, "session not found")
	}
	rec.Title = strings.TrimSpace(title)
	rec.UpdatedAt = time.Now().UTC()
	if err := m.cfg.Catalog.UpdateSession(ctx, rec); err != nil {
		return SessionView{}, err
	}
	m.mu.Lock()
	if slot, exists := m.workers[sessionID]; exists {
		slot.record = rec
	}
	m.mu.Unlock()
	return recordToView(rec, StateUnloaded, false), nil
}

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
		if entry, ok := m.idempotency[key]; ok && time.Now().Before(entry.expiresAt) {
			runID := entry.runID
			m.mu.Unlock()
			return RunView{RunID: runID, Accepted: true}, nil
		}
		m.mu.Unlock()
	}

	slot, err := m.loadSession(ctx, cmd.SessionID)
	if err != nil {
		return RunView{}, err
	}
	if slot.worker.IsBusy() {
		return RunView{}, fmtError(CodeBusy, "The session is already running.")
	}

	// Validate document context if provided.
	docID := strings.TrimSpace(cmd.DocumentID)
	if docID != "" {
		if m.cfg.Tools == nil {
			return RunView{}, fmtError(CodeInvalidRequest, "document tools unavailable")
		}
		if _, err := m.cfg.Tools.GetDocument(ctx, docID); err != nil {
			return RunView{}, fmtError(CodeInvalidRequest, "document context not found")
		}
		// Stash context for the extension's before_agent_start fetch.
		m.storeRunContext(cmd.SessionID, docID)
	}

	runID := newEntityID()
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

func (m *manager) Abort(ctx context.Context, sessionID, runID, clientID string) error {
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	state, _, _, currentRun, _, _, _ := slot.worker.SnapshotInfo()
	if currentRun != "" && runID != "" && currentRun != runID {
		return fmtError(CodeInvalidRequest, "run is not active")
	}
	_ = state
	_ = clientID // any authenticated client may abort; UI must warn
	return slot.worker.abort(ctx)
}

func (m *manager) Claim(ctx context.Context, sessionID, runID, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok || run.SessionID != sessionID {
		return fmtError(CodeInvalidRequest, "run not found")
	}
	// Only allow claim when controller lease expired (30s).
	if time.Since(run.ControllerSeen) < 30*time.Second && run.ControllerID != clientID {
		return fmtError(CodeNotRunController, "controller lease still active")
	}
	run.ControllerID = clientID
	run.ControllerSeen = time.Now()
	if slot, ok := m.workers[sessionID]; ok && slot.worker != nil {
		slot.worker.mu.Lock()
		slot.worker.clientID = clientID
		slot.worker.mu.Unlock()
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

func (m *manager) ResolveApproval(ctx context.Context, cmd ResolveApprovalCommand) error {
	m.mu.Lock()
	pending, ok := m.approvals[cmd.ApprovalID]
	if !ok {
		m.mu.Unlock()
		return fmtError(CodeApprovalExpired, "approval not found")
	}
	if pending.Resolved {
		if pending.Decision == cmd.Decision {
			m.mu.Unlock()
			return nil // idempotent
		}
		m.mu.Unlock()
		return fmtError(CodeApprovalResolved, "approval already resolved")
	}
	if pending.SessionID != cmd.SessionID || pending.RunID != cmd.RunID {
		m.mu.Unlock()
		return fmtError(CodeInvalidRequest, "approval mismatch")
	}
	if pending.ControllerID != cmd.ClientID {
		// Allow claim-expired controllers via Claim first.
		run := m.runs[cmd.RunID]
		if run == nil || run.ControllerID != cmd.ClientID {
			m.mu.Unlock()
			return fmtError(CodeNotRunController, "only the run controller may resolve approvals")
		}
	}
	if time.Now().After(pending.ExpiresAt) {
		pending.Resolved = true
		pending.Decision = "deny"
		m.mu.Unlock()
		_ = pending.Worker.respondExtensionUI(ctx, pending.PiRequestID, map[string]any{"confirmed": false})
		return fmtError(CodeApprovalExpired, "approval expired")
	}
	pending.Resolved = true
	pending.Decision = cmd.Decision
	worker := pending.Worker
	piID := pending.PiRequestID
	sessionID := pending.SessionID
	runID := pending.RunID
	m.mu.Unlock()

	confirmed := cmd.Decision == "allow_once"
	if err := worker.respondExtensionUI(ctx, piID, map[string]any{"confirmed": confirmed}); err != nil {
		return err
	}
	worker.stream.Publish(Event{
		SessionID: sessionID,
		RunID:     runID,
		Type:      EventApprovalResolved,
		Payload: payloadObject(map[string]any{
			"approval_id": cmd.ApprovalID,
			"decision":    cmd.Decision,
		}),
	})
	worker.mu.Lock()
	if worker.state == StateWaitingApproval {
		worker.state = StateRunning
	}
	worker.mu.Unlock()
	return nil
}

func (m *manager) ListModels(ctx context.Context) ([]ModelRef, error) {
	if err := m.ensureProbed(ctx); err != nil {
		return nil, err
	}
	// Use any loaded worker, or spin a temporary probe worker.
	m.mu.Lock()
	for _, slot := range m.workers {
		if slot.worker != nil {
			w := slot.worker
			m.mu.Unlock()
			return w.getAvailableModels(ctx)
		}
	}
	m.mu.Unlock()
	// Temporary worker for model listing.
	stream := newEventStream("probe")
	w, err := m.spawnWorker(ctx, "probe-"+newEntityID()[:8], SessionRecord{Title: "probe"}, "", stream, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = w.Stop(ctx) }()
	return w.getAvailableModels(ctx)
}

func (m *manager) SetModel(ctx context.Context, sessionID string, model ModelRef) error {
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if slot.worker.IsBusy() {
		return fmtError(CodeBusy, "cannot change model while running")
	}
	if err := slot.worker.setModel(ctx, model); err != nil {
		return err
	}
	slot.record.ModelProvider = model.Provider
	slot.record.ModelID = model.ID
	slot.record.UpdatedAt = time.Now().UTC()
	return m.cfg.Catalog.UpdateSession(ctx, slot.record)
}

func (m *manager) SetThinking(ctx context.Context, sessionID, level string) error {
	slot, err := m.loadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if slot.worker.IsBusy() {
		return fmtError(CodeBusy, "cannot change thinking level while running")
	}
	if err := slot.worker.setThinking(ctx, level); err != nil {
		return err
	}
	slot.record.ThinkingLevel = level
	slot.record.UpdatedAt = time.Now().UTC()
	return m.cfg.Catalog.UpdateSession(ctx, slot.record)
}

func (m *manager) Doctor(ctx context.Context) (DoctorReport, error) {
	report := DoctorReport{
		WriteToolsEnabled: m.cfg.WriteTools,
		SessionDir:        m.sessionRoot,
	}
	if err := os.MkdirAll(m.sessionRoot, 0o700); err == nil {
		report.SessionDirOK = true
	}
	if err := m.ensureProbed(ctx); err != nil {
		report.Error = err.Error()
		report.Notes = append(report.Notes, "Pi capability probe failed")
		return report, nil
	}
	m.mu.Lock()
	report.PiPath = m.piPath
	report.PiVersion = m.piVersion
	report.ExtensionOK = m.extensionPath != ""
	report.PiCompatible = m.piVersion != ""
	m.mu.Unlock()
	report.CompanionReachable = true
	models, err := m.ListModels(ctx)
	if err != nil {
		report.Notes = append(report.Notes, "model list failed: "+err.Error())
		report.Notes = append(report.Notes, "If authentication is required, complete Pi login in a separate terminal.")
	} else {
		report.ModelsAvailable = len(models) > 0
		if !report.ModelsAvailable {
			report.Notes = append(report.Notes, "No models configured. Authenticate providers via the pi CLI.")
		}
	}
	if !m.cfg.WriteTools {
		report.Notes = append(report.Notes, "Write tools disabled (read-only Agent preview).")
	}
	return report, nil
}

func (m *manager) Shutdown(ctx context.Context) error {
	m.cancel()
	m.mu.Lock()
	slots := make([]*sessionSlot, 0, len(m.workers))
	for id, slot := range m.workers {
		slots = append(slots, slot)
		delete(m.workers, id)
	}
	m.mu.Unlock()
	var errs []error
	for _, slot := range slots {
		if slot.worker != nil {
			if err := slot.worker.Stop(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if slot.stream != nil {
			slot.stream.Close()
		}
	}
	m.wg.Wait()
	return errors.Join(errs...)
}

// --- internal helpers ---

func (m *manager) loadSession(ctx context.Context, sessionID string) (*sessionSlot, error) {
	if m.cfg.Catalog == nil {
		return nil, fmtError(CodeInternal, "session catalog not configured")
	}
	m.mu.Lock()
	if slot, ok := m.workers[sessionID]; ok && slot.worker != nil {
		slot.lastUsed = time.Now()
		m.mu.Unlock()
		return slot, nil
	}
	m.mu.Unlock()

	rec, ok, err := m.cfg.Catalog.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmtError(CodeSessionNotFound, "session not found")
	}
	if _, err := os.Stat(rec.SessionPath); err != nil {
		return nil, fmtError(CodeSessionFileMissing, "session file missing")
	}
	if _, err := validateSessionPath(m.sessionRoot, rec.SessionPath); err != nil {
		return nil, err
	}
	if err := m.ensureCapacity(ctx); err != nil {
		return nil, err
	}
	stream := newEventStream(sessionID)
	worker, err := m.spawnWorker(ctx, sessionID, rec, rec.SessionPath, stream, nil)
	if err != nil {
		return nil, err
	}
	piSID, _ := worker.PiSession()
	if rec.PiSessionID != "" && piSID != "" && piSID != rec.PiSessionID {
		_ = worker.Stop(ctx)
		return nil, fmtError(CodeSessionFileMissing, "pi session id mismatch")
	}
	slot := &sessionSlot{record: rec, worker: worker, stream: stream, lastUsed: time.Now()}
	m.mu.Lock()
	// Double-check race: another loader may have won.
	if existing, ok := m.workers[sessionID]; ok && existing.worker != nil {
		m.mu.Unlock()
		_ = worker.Stop(ctx)
		stream.Close()
		return existing, nil
	}
	m.workers[sessionID] = slot
	m.mu.Unlock()
	return slot, nil
}

func (m *manager) spawnWorker(ctx context.Context, sessionID string, rec SessionRecord, sessionPath string, stream *eventStream, model *ModelRef) (*worker, error) {
	m.mu.Lock()
	piPath := m.piPath
	extPath := m.extensionPath
	m.mu.Unlock()

	token := randomToken(24)
	workerID := newEntityID()
	baseURL := ""
	if m.cfg.InternalBaseURL != nil {
		baseURL = m.cfg.InternalBaseURL()
	}
	tools := ToolNamesReadOnly()
	if m.cfg.WriteTools {
		tools = ToolNamesAll()
	}
	cfg := workerConfig{
		PiPath:          piPath,
		SessionDir:      m.sessionRoot,
		SessionPath:     sessionPath,
		ExtensionPath:   extPath,
		Tools:           tools,
		Home:            m.cfg.Home,
		InternalBaseURL: baseURL,
		InternalToken:   token,
		WorkerID:        workerID,
		SessionID:       sessionID,
		ThinkingLevel:   rec.ThinkingLevel,
		Title:           rec.Title,
	}
	if model != nil {
		cfg.ModelProvider = model.Provider
		cfg.ModelID = model.ID
	} else if rec.ModelProvider != "" {
		cfg.ModelProvider = rec.ModelProvider
		cfg.ModelID = rec.ModelID
	}
	w := newWorker(m.ctx, cfg, stream)
	// Register internal credentials before Start so extension can call back.
	registerWorkerAuth(workerID, sessionID, token, m)
	w.onSettled = func(runID string, interrupted bool) {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = interrupted
	}
	// Capture extension UI approvals into manager map.
	// Monkey-patch via stream listener? worker already publishes approval.requested;
	// we intercept by wrapping handleExtensionUI through a callback.
	w.mu.Lock()
	// Install approval bridge after start by watching stream — simpler: override via onApproval.
	w.mu.Unlock()

	if err := w.Start(); err != nil {
		revokeWorkerAuth(workerID)
		return nil, err
	}
	// Hook approvals: re-bind handle by storing manager reference on worker via stream events.
	// We register a side channel: when approval.requested is published, manager records it.
	// Done by wrapping Publish — instead, poll is wrong. Attach callback:
	m.attachApprovalBridge(w, sessionID)
	return w, nil
}

func (m *manager) attachApprovalBridge(w *worker, sessionID string) {
	// Replace stream publish is hard; instead wrap worker's handle by storing manager.
	// We intercept at ResolveApproval time from events already published.
	// Record pending approvals when events of type approval.requested appear —
	// subscribe internally once.
	go func() {
		sub, err := w.stream.Subscribe(m.ctx, "")
		if err != nil {
			return
		}
		defer sub.Close()
		for {
			select {
			case <-m.ctx.Done():
				return
			case ev, ok := <-sub.Events():
				if !ok {
					return
				}
				if ev.Type != EventApprovalRequested {
					continue
				}
				var payload struct {
					ApprovalID   string `json:"approval_id"`
					ControllerID string `json:"controller_id"`
					TimeoutMS    int    `json:"timeout_ms"`
				}
				_ = json.Unmarshal(ev.Payload, &payload)
				timeout := 60 * time.Second
				if payload.TimeoutMS > 0 {
					timeout = time.Duration(payload.TimeoutMS) * time.Millisecond
				}
				m.mu.Lock()
				m.approvals[payload.ApprovalID] = &pendingApproval{
					SessionID:    sessionID,
					RunID:        ev.RunID,
					ControllerID: payload.ControllerID,
					Worker:       w,
					PiRequestID:  payload.ApprovalID,
					ExpiresAt:    time.Now().Add(timeout),
				}
				m.mu.Unlock()
			}
		}
	}()
}

func (m *manager) ensureCapacity(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	loaded := 0
	for _, slot := range m.workers {
		if slot.worker != nil {
			loaded++
		}
	}
	if loaded < m.cfg.MaxWorkers {
		return nil
	}
	// Evict longest-idle unloaded-eligible worker.
	var (
		victimID string
		victim   *sessionSlot
		oldest   time.Time
	)
	for id, slot := range m.workers {
		if slot.worker == nil {
			continue
		}
		if slot.worker.IsBusy() {
			continue
		}
		if slot.stream != nil && slot.stream.SubscriberCount() > 0 {
			continue
		}
		idleSince := slot.worker.IdleSince()
		if victimID == "" || idleSince.Before(oldest) {
			victimID, victim, oldest = id, slot, idleSince
		}
	}
	if victim == nil {
		return fmtError(CodeCapacityReached, "agent worker capacity reached")
	}
	delete(m.workers, victimID)
	go func() {
		_ = victim.worker.Stop(ctx)
		victim.stream.Close()
	}()
	return nil
}

func (m *manager) evictionLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.evictIdle()
		}
	}
}

func (m *manager) evictIdle() {
	m.mu.Lock()
	var victims []*sessionSlot
	var ids []string
	now := time.Now()
	for id, slot := range m.workers {
		if slot.worker == nil {
			continue
		}
		if slot.worker.IsBusy() {
			continue
		}
		if slot.stream != nil && slot.stream.SubscriberCount() > 0 {
			continue
		}
		if now.Sub(slot.worker.IdleSince()) >= m.cfg.IdleTimeout {
			victims = append(victims, slot)
			ids = append(ids, id)
			delete(m.workers, id)
		}
	}
	m.mu.Unlock()
	for _, slot := range victims {
		_ = slot.worker.Stop(context.Background())
		slot.stream.Close()
	}
}

// Run context stash for extension before_agent_start.
var (
	runContextMu sync.Mutex
	runContexts  = map[string]runContextEntry{} // sessionID -> context
)

type runContextEntry struct {
	DocumentID string
	Title      string
	Status     string
	ExpiresAt  time.Time
}

func (m *manager) storeRunContext(sessionID, documentID string) {
	title, status := "", ""
	if m.cfg.Tools != nil {
		if view, err := m.cfg.Tools.GetDocument(context.Background(), documentID); err == nil {
			title, status = view.Title, view.Status
		}
	}
	runContextMu.Lock()
	runContexts[sessionID] = runContextEntry{
		DocumentID: documentID,
		Title:      title,
		Status:     status,
		ExpiresAt:  time.Now().Add(5 * time.Minute),
	}
	runContextMu.Unlock()
}

// TakeRunContext is called once by the internal HTTP handler for the extension.
func TakeRunContext(sessionID string) (runContextEntry, bool) {
	runContextMu.Lock()
	defer runContextMu.Unlock()
	entry, ok := runContexts[sessionID]
	if !ok {
		return runContextEntry{}, false
	}
	delete(runContexts, sessionID)
	if time.Now().After(entry.ExpiresAt) {
		return runContextEntry{}, false
	}
	return entry, true
}

// Worker auth registry for internal endpoints.
type workerAuth struct {
	SessionID string
	Token     string
	Manager   *manager
}

var (
	workerAuthMu sync.Mutex
	workerAuths  = map[string]workerAuth{} // workerID -> auth
)

func registerWorkerAuth(workerID, sessionID, token string, m *manager) {
	workerAuthMu.Lock()
	workerAuths[workerID] = workerAuth{SessionID: sessionID, Token: token, Manager: m}
	workerAuthMu.Unlock()
}

func revokeWorkerAuth(workerID string) {
	workerAuthMu.Lock()
	delete(workerAuths, workerID)
	workerAuthMu.Unlock()
}

// LookupWorkerAuth validates an internal worker request.
func LookupWorkerAuth(workerID, sessionID, token string) (InternalManager, bool) {
	workerAuthMu.Lock()
	defer workerAuthMu.Unlock()
	auth, ok := workerAuths[workerID]
	if !ok {
		return nil, false
	}
	if auth.SessionID != sessionID || auth.Token == "" || auth.Token != token {
		return nil, false
	}
	return auth.Manager, true
}

// ManagerHandle is a validated internal worker identity.
type ManagerHandle struct {
	Manager   InternalManager
	SessionID string
	WorkerID  string
}

// InternalManager is the subset of manager used by internal HTTP handlers.
type InternalManager interface {
	ToolsFor() DocumentTools
	WriteToolsEnabled() bool
}

// ToolsFor returns document tools from a manager (for internal HTTP).
func (m *manager) ToolsFor() DocumentTools { return m.cfg.Tools }

func (m *manager) WriteToolsEnabled() bool { return m.cfg.WriteTools }

// AsInternal exposes the concrete manager as InternalManager after auth.
func (m *manager) AsInternal() InternalManager { return m }

// create-session idempotency (stores full snapshot briefly)
var (
	createIdemMu sync.Mutex
	createIdem   = map[string]struct {
		snap SessionSnapshot
		exp  time.Time
	}{}
)

func (m *manager) lookupCreateIdempotency(clientID, key string) (SessionSnapshot, bool) {
	createIdemMu.Lock()
	defer createIdemMu.Unlock()
	entry, ok := createIdem[clientID+"|"+key]
	if !ok || time.Now().After(entry.exp) {
		return SessionSnapshot{}, false
	}
	return entry.snap, true
}

func (m *manager) storeCreateIdempotency(clientID, key string, snap SessionSnapshot) {
	createIdemMu.Lock()
	defer createIdemMu.Unlock()
	createIdem[clientID+"|"+key] = struct {
		snap SessionSnapshot
		exp  time.Time
	}{snap: snap, exp: time.Now().Add(10 * time.Minute)}
}

func (m *manager) pruneIdempotencyLocked() {
	now := time.Now()
	for k, v := range m.idempotency {
		if now.After(v.expiresAt) {
			delete(m.idempotency, k)
		}
	}
	// Bound map size.
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

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		id, _ := uuid.NewV7()
		return id.String()
	}
	return hex.EncodeToString(b)
}

func nilString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
