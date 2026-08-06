package agent

import (
	"context"
	"time"
)

// pendingApproval is one write-tool confirmation awaiting a controller
// decision. Workers register these via onApprovalRequested; ResolveApproval
// answers them and relays the response into the Pi RPC process.
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

// ResolveApproval answers one confirmation. Only the run controller may
// decide; repeated identical decisions are idempotent, opposite ones conflict.
// Expiry always resolves as deny — never allow.
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
	sessionID := pending.SessionID
	runID := pending.RunID
	m.mu.Unlock()

	confirmed := cmd.Decision == "allow_once"
	if err := worker.respondExtensionUI(ctx, cmd.ApprovalID, map[string]any{"confirmed": confirmed}); err != nil {
		return err
	}
	worker.Stream().Publish(Event{
		SessionID: sessionID,
		RunID:     runID,
		Type:      EventApprovalResolved,
		Payload: payloadObject(map[string]any{
			"approval_id": cmd.ApprovalID,
			"decision":    cmd.Decision,
		}),
	})
	worker.ResumeAfterApproval()
	return nil
}
