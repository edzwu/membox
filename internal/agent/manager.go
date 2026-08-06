package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Minimum Pi version accepted by the capability probe.
// Locked at implementation time; bump after contract tests pass on newer Pi.
const MinPiVersion = "0.1.0"

// Config configures the AgentManager.
type Config struct {
	Home            string
	Enabled         bool
	PiPath          string // empty = PATH lookup
	MaxWorkers      int
	IdleTimeout     time.Duration
	WriteTools      bool // V1 default false
	InternalBaseURL func() string
	// ExtensionSource is the TypeScript source materialized at startup.
	ExtensionSource []byte
	Catalog         SessionCatalog
	Tools           DocumentTools
	// Settings lookup (optional).
	GetSetting func(ctx context.Context, key string) (string, error)
}

// manager is the concrete AgentManager. All mutable state lives on the struct
// (no package globals) so one Companion owns exactly one control plane.
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

	// Transient control-plane state, bounded and pruned.
	idempotency map[string]idempotencyEntry // client|session|key -> runID
	approvals   map[string]*pendingApproval // approvalID -> pending
	runs        map[string]*runState        // runID -> state
	workerAuths map[string]workerAuth       // workerID -> auth
	runContexts map[string]runContextEntry  // sessionID -> pending turn context
	createIdem  map[string]createIdemEntry  // client|key -> snapshot

	probedAt time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type sessionSlot struct {
	record   SessionRecord
	worker   *worker
	stream   *eventStream
	lastUsed time.Time
}

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
		workerAuths: make(map[string]workerAuth),
		runContexts: make(map[string]runContextEntry),
		createIdem:  make(map[string]createIdemEntry),
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
	for workerID := range m.workerAuths {
		delete(m.workerAuths, workerID)
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

// loadSession returns the live slot for a session, spawning its worker when
// needed. Concurrent loaders race safely: the loser stops its worker and
// joins the winner's slot.
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

// spawnWorker starts one Pi RPC process bound to sessionID and registers its
// internal credentials plus approval routing before the process runs.
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
		WriteTools:      m.cfg.WriteTools,
	}
	if model != nil {
		cfg.ModelProvider = model.Provider
		cfg.ModelID = model.ID
	} else if rec.ModelProvider != "" {
		cfg.ModelProvider = rec.ModelProvider
		cfg.ModelID = rec.ModelID
	}
	w := newWorker(m.ctx, cfg, stream)
	w.onSettled = func(runID string, _ bool) {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
	}
	w.onApprovalRequested = func(req ApprovalRequest) {
		timeout := 60 * time.Second
		if req.TimeoutMS > 0 {
			timeout = time.Duration(req.TimeoutMS) * time.Millisecond
		}
		m.mu.Lock()
		m.approvals[req.ApprovalID] = &pendingApproval{
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			ControllerID: req.ControllerID,
			Worker:       w,
			PiRequestID:  req.ApprovalID,
			ExpiresAt:    time.Now().Add(timeout),
		}
		m.mu.Unlock()
	}
	m.registerWorkerAuth(workerID, sessionID, token)

	if err := w.Start(); err != nil {
		m.revokeWorkerAuth(workerID)
		return nil, err
	}
	return w, nil
}

// ToolsFor exposes document tools to the internal HTTP adapter.
func (m *manager) ToolsFor() DocumentTools { return m.cfg.Tools }

// WriteToolsEnabled reports whether write tools are active for this manager.
func (m *manager) WriteToolsEnabled() bool { return m.cfg.WriteTools }
