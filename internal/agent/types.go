package agent

import (
	"context"
	"encoding/json"
	"time"
)

// Manager is the Companion-owned control plane for Pi RPC workers.
type Manager interface {
	Status(ctx context.Context) (Status, error)
	ListSessions(ctx context.Context, q ListSessionsQuery) ([]SessionView, error)
	CreateSession(ctx context.Context, cmd CreateSessionCommand) (SessionSnapshot, error)
	Snapshot(ctx context.Context, sessionID string) (SessionSnapshot, error)
	ArchiveSession(ctx context.Context, sessionID string) error
	RenameSession(ctx context.Context, sessionID, title string) (SessionView, error)
	Prompt(ctx context.Context, cmd PromptCommand) (RunView, error)
	Abort(ctx context.Context, sessionID, runID, clientID string) error
	Claim(ctx context.Context, sessionID, runID, clientID string) error
	Subscribe(ctx context.Context, sessionID, afterEventID string) (Subscription, error)
	ResolveApproval(ctx context.Context, cmd ResolveApprovalCommand) error
	ListModels(ctx context.Context) ([]ModelRef, error)
	SetModel(ctx context.Context, sessionID string, model ModelRef) error
	SetThinking(ctx context.Context, sessionID, level string) error
	Doctor(ctx context.Context) (DoctorReport, error)
	Shutdown(ctx context.Context) error

	// Internal worker API (used by the Companion's worker-only HTTP adapter).
	ValidateWorker(workerID, sessionID, token string) bool
	TakeRunContext(sessionID string) (RunContext, bool)
	ToolsFor() DocumentTools
	WriteToolsEnabled() bool
}

// ManagerHandle is a validated internal worker identity handed to the
// worker-only HTTP handlers.
type ManagerHandle struct {
	Manager   Manager
	SessionID string
	WorkerID  string
}

// Status is the lightweight capability probe result.
type Status struct {
	Enabled       bool    `json:"enabled"`
	Available     bool    `json:"available"`
	PiPath        string  `json:"pi_path,omitempty"`
	PiVersion     string  `json:"pi_version,omitempty"`
	State         string  `json:"state"`
	LoadedWorkers int     `json:"loaded_workers"`
	MaxWorkers    int     `json:"max_workers"`
	WriteTools    bool    `json:"write_tools"`
	Error         *string `json:"error"`
}

// ListSessionsQuery filters the session catalog.
type ListSessionsQuery struct {
	IncludeArchived bool
}

// CreateSessionCommand creates a new Pi-backed session.
type CreateSessionCommand struct {
	Title          string
	Model          *ModelRef
	ThinkingLevel  string
	IdempotencyKey string
	ClientID       string
}

// PromptCommand submits one user turn.
type PromptCommand struct {
	SessionID      string
	ClientID       string
	IdempotencyKey string
	Text           string
	DocumentID     string
}

// ResolveApprovalCommand answers one write confirmation.
type ResolveApprovalCommand struct {
	SessionID  string
	RunID      string
	ApprovalID string
	ClientID   string
	Decision   string // allow_once | deny
}

// ApprovalRequest is the structured payload a worker delivers to the manager
// when a write tool asks for user confirmation.
type ApprovalRequest struct {
	ApprovalID   string
	SessionID    string
	RunID        string
	ControllerID string
	Title        string
	Message      string
	TimeoutMS    int
}

// RunContext is the per-turn document context the extension fetches once at
// before_agent_start.
type RunContext struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

// ModelRef identifies a configured Pi model.
type ModelRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
}

// SessionView is catalog metadata for list/picker UIs.
type SessionView struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	Model         *ModelRef `json:"model,omitempty"`
	ThinkingLevel string    `json:"thinking_level,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
	Archived      bool      `json:"archived"`
	Unavailable   bool      `json:"unavailable,omitempty"`
}

// RunView is the accepted prompt acknowledgement.
type RunView struct {
	RunID    string `json:"run_id"`
	Accepted bool   `json:"accepted"`
}

// ActiveRun describes the in-flight turn for a session snapshot.
type ActiveRun struct {
	ID           string    `json:"id"`
	ControllerID string    `json:"controller_id"`
	StartedAt    time.Time `json:"started_at"`
	DocumentID   string    `json:"document_id,omitempty"`
	State        string    `json:"state"`
}

// SessionSnapshot is the reconnect recovery payload.
type SessionSnapshot struct {
	Session     SessionView       `json:"session"`
	Messages    []json.RawMessage `json:"messages"`
	ActiveRun   *ActiveRun        `json:"active_run"`
	LastEventID string            `json:"last_event_id"`
	StreamEpoch string            `json:"stream_epoch"`
}

// Event is the normalized public event model.
type Event struct {
	Version   int             `json:"v"`
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id,omitempty"`
	Type      string          `json:"type"`
	At        time.Time       `json:"at"`
	Payload   json.RawMessage `json:"payload"`
}

// Subscription delivers normalized session events.
type Subscription interface {
	Events() <-chan Event
	Close() error
}

// DoctorReport is the mm agent doctor payload.
type DoctorReport struct {
	CompanionReachable bool     `json:"companion_reachable"`
	PiPath             string   `json:"pi_path,omitempty"`
	PiVersion          string   `json:"pi_version,omitempty"`
	PiCompatible       bool     `json:"pi_compatible"`
	SessionDir         string   `json:"session_dir,omitempty"`
	SessionDirOK       bool     `json:"session_dir_ok"`
	ExtensionOK        bool     `json:"extension_ok"`
	ModelsAvailable    bool     `json:"models_available"`
	WriteToolsEnabled  bool     `json:"write_tools_enabled"`
	Notes              []string `json:"notes,omitempty"`
	Error              string   `json:"error,omitempty"`
}

// Public event type constants.
const (
	EventStreamReset        = "stream.reset"
	EventSessionState       = "session.state"
	EventRunStarted         = "run.started"
	EventRunSettled         = "run.settled"
	EventRunInterrupted     = "run.interrupted"
	EventUserAccepted       = "user.accepted"
	EventAssistantStarted   = "assistant.started"
	EventAssistantDelta     = "assistant.delta"
	EventAssistantCompleted = "assistant.completed"
	EventToolStarted        = "tool.started"
	EventToolProgress       = "tool.progress"
	EventToolCompleted      = "tool.completed"
	EventApprovalRequested  = "approval.requested"
	EventApprovalResolved   = "approval.resolved"
	EventQueueUpdated       = "queue.updated"
	EventModelChanged       = "model.changed"
	EventUsageUpdated       = "usage.updated"
	EventAgentError         = "agent.error"
)

// Session/worker state constants.
const (
	StateDisabled        = "disabled"
	StateProbing         = "probing"
	StateReady           = "ready"
	StateUnavailable     = "unavailable"
	StateUnloaded        = "unloaded"
	StateStarting        = "starting"
	StateIdle            = "idle"
	StateRunning         = "running"
	StateFailed          = "failed"
	StateRestarting      = "restarting"
	StateInterrupted     = "interrupted"
	StateWaitingApproval = "waiting_approval"
)
