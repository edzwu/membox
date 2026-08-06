package agent

import (
	"errors"
	"fmt"
)

// Stable error codes exposed over HTTP and diagnostics.
const (
	CodeDisabled            = "agent_disabled"
	CodeUnavailable         = "agent_unavailable"
	CodeIncompatiblePi      = "agent_incompatible_pi"
	CodeNotAuthenticated    = "agent_not_authenticated"
	CodeBusy                = "agent_busy"
	CodeCapacityReached     = "agent_capacity_reached"
	CodeSessionNotFound     = "session_not_found"
	CodeSessionFileMissing  = "session_file_missing"
	CodeWorkerStartFailed   = "worker_start_failed"
	CodeWorkerExited        = "worker_exited"
	CodeProtocolError       = "protocol_error"
	CodeStreamReplayUnavail = "stream_replay_unavailable"
	CodeNotRunController    = "not_run_controller"
	CodeApprovalExpired     = "approval_expired"
	CodeApprovalResolved    = "approval_already_resolved"
	CodeRevisionConflict    = "revision_conflict"
	CodeMutationDenied      = "mutation_denied"
	CodeWriteToolsDisabled  = "write_tools_disabled"
	CodeInvalidRequest      = "invalid_request"
	CodeInternal            = "internal_error"
)

// Error is a stable, code-bearing agent error.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e.Code == other.Code
}

func newError(code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

func wrapError(code, message string, err error) error {
	if err == nil {
		return newError(code, message, nil)
	}
	var existing *Error
	if errors.As(err, &existing) {
		return existing
	}
	return newError(code, message, err)
}

func fmtError(code, format string, args ...any) *Error {
	return newError(code, fmt.Sprintf(format, args...), nil)
}

// CodeOf extracts a stable agent error code when present.
func CodeOf(err error) string {
	var agentErr *Error
	if errors.As(err, &agentErr) {
		return agentErr.Code
	}
	return CodeInternal
}

// Constructors for HTTP/adapters.

func NewDisabled() error {
	return newError(CodeDisabled, "agent is disabled", nil)
}

func NewInvalid(message string) error {
	return newError(CodeInvalidRequest, message, nil)
}

func NewWriteDisabled() error {
	return newError(CodeWriteToolsDisabled, "write tools are disabled", nil)
}
