package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// workerAuth binds one Pi worker process to its internal API credentials.
// Entries live and die with the worker: revoked on spawn failure, dropped on
// Shutdown. Validation checks worker + session + token together.
type workerAuth struct {
	SessionID string
	Token     string
}

func (m *manager) registerWorkerAuth(workerID, sessionID, token string) {
	m.mu.Lock()
	m.workerAuths[workerID] = workerAuth{SessionID: sessionID, Token: token}
	m.mu.Unlock()
}

func (m *manager) revokeWorkerAuth(workerID string) {
	m.mu.Lock()
	delete(m.workerAuths, workerID)
	m.mu.Unlock()
}

// ValidateWorker authenticates an internal request from a Pi worker process.
func (m *manager) ValidateWorker(workerID, sessionID, token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	auth, ok := m.workerAuths[workerID]
	if !ok || auth.Token == "" {
		return false
	}
	return auth.SessionID == sessionID && auth.Token == token
}

// runContextEntry is the pending per-turn document context with expiry.
type runContextEntry struct {
	RunContext
	ExpiresAt time.Time
}

// storeRunContext stashes the turn's document context for the extension's
// one-shot before_agent_start fetch.
func (m *manager) storeRunContext(sessionID, documentID string) {
	title, status := "", ""
	if m.cfg.Tools != nil {
		if view, err := m.cfg.Tools.GetDocument(context.Background(), documentID); err == nil {
			title, status = view.Title, view.Status
		}
	}
	m.mu.Lock()
	m.runContexts[sessionID] = runContextEntry{
		RunContext: RunContext{DocumentID: documentID, Title: title, Status: status},
		ExpiresAt:  time.Now().Add(5 * time.Minute),
	}
	m.mu.Unlock()
}

// TakeRunContext consumes the pending turn context exactly once.
func (m *manager) TakeRunContext(sessionID string) (RunContext, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.runContexts[sessionID]
	if !ok {
		return RunContext{}, false
	}
	delete(m.runContexts, sessionID)
	if time.Now().After(entry.ExpiresAt) {
		return RunContext{}, false
	}
	return entry.RunContext, true
}

// createIdemEntry makes session creation idempotent per client+key.
type createIdemEntry struct {
	snap SessionSnapshot
	exp  time.Time
}

func (m *manager) lookupCreateIdempotency(clientID, key string) (SessionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.createIdem[clientID+"|"+key]
	if !ok || time.Now().After(entry.exp) {
		return SessionSnapshot{}, false
	}
	return entry.snap, true
}

func (m *manager) storeCreateIdempotency(clientID, key string, snap SessionSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createIdem[clientID+"|"+key] = createIdemEntry{snap: snap, exp: time.Now().Add(10 * time.Minute)}
	if len(m.createIdem) > 256 {
		for k, v := range m.createIdem {
			if time.Now().After(v.exp) {
				delete(m.createIdem, k)
			}
		}
	}
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

// writeToolsFlag formats the worker env var the embedded extension checks.
func writeToolsFlag(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

func newEntityID() string {
	return newCommandID()
}
