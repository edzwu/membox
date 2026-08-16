// Package companion manages the membox Web Companion: the single process per
// MEMBOX_HOME that owns the HTTP reader, the browser bridge, and data writes
// from the web. The TUI and CLI never embed the server themselves; they probe
// a running companion or start one and then control it over its token-authed
// control endpoints.
package companion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/web/backend"
)

// Lifecycle mode mirrors the backend constant so callers need only this package.
const LifecycleKeep = backend.CompanionLifecycleKeep

// ErrAlreadyRunning indicates another companion holds the singleton lock.
var ErrAlreadyRunning = errors.New("a web companion is already running")

// Status is the control-plane view of the companion for one MEMBOX_HOME.
type Status struct {
	Running   bool
	BaseURL   string
	Port      int
	Token     string
	Mode      string
	PID       int
	Tabs      int
	DirtyTabs int
	StartedAt time.Time
	// Started reports that the last Ensure call spawned a new process.
	Started bool
}

// LockPath is the singleton lock file guarding one companion per home.
func LockPath(home string) string { return filepath.Join(home, "companion.lock") }

// LogPath collects the detached companion's stdout/stderr for diagnostics.
func LogPath(home string) string { return filepath.Join(home, "companion.log") }

// Probe reports whether a companion answers on the address recorded in
// bridge.json. A stale file (server died) reports Running=false, never an
// error: callers treat "not running" as the cue to start one.
func Probe(ctx context.Context, home string) (Status, error) {
	bridge, err := backend.ReadBridgeFile(home)
	if err != nil {
		return Status{}, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(bridge.BaseURL), "/")
	if baseURL == "" {
		return Status{}, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/api/companion/status", nil)
	if err != nil {
		return Status{}, nil
	}
	if strings.TrimSpace(bridge.Token) != "" {
		request.Header.Set("X-Membox-Token", bridge.Token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Status{}, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Status{}, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Status{}, nil
	}
	var payload struct {
		Running   bool   `json:"running"`
		PID       int    `json:"pid"`
		Mode      string `json:"mode"`
		StartedAt string `json:"started_at"`
		Tabs      int    `json:"tabs"`
		DirtyTabs int    `json:"dirty_tabs"`
		Port      int    `json:"port"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.Running {
		return Status{}, nil
	}
	startedAt, _ := time.Parse(time.RFC3339, payload.StartedAt)
	port := payload.Port
	if port == 0 {
		port = bridge.Port
	}
	return Status{
		Running:   true,
		BaseURL:   baseURL,
		Port:      port,
		Token:     bridge.Token,
		Mode:      payload.Mode,
		PID:       payload.PID,
		Tabs:      payload.Tabs,
		DirtyTabs: payload.DirtyTabs,
		StartedAt: startedAt.Local(),
	}, nil
}

// SpawnFunc starts a detached companion process and returns its PID. The TUI
// injects its own spawn in tests; production uses SpawnDetached.
type SpawnFunc func(ctx context.Context, home, lifecycle string, port int) (int, error)

// Ensure probes first, then spawns a companion when none answers. It returns
// the live status and whether this call started the process. A held lock with
// no answering server means a companion is still booting, so Ensure waits for
// it instead of starting a second one.
//
// A running companion whose binary no longer matches the current executable is
// stopped and replaced: keep-mode processes live long enough to go stale after
// a rebuild, and reusing one was the root cause of "assist/deepseek empty" and
// other old-binary bugs.
func Ensure(ctx context.Context, home, lifecycle string, port int, spawn SpawnFunc) (Status, bool, error) {
	if lifecycle == "" {
		lifecycle = LifecycleKeep
	}
	if status, _ := Probe(ctx, home); status.Running {
		if binaryCurrent(home) {
			return status, false, nil
		}
		// Stale binary: recycle it before returning a live status.
		_ = stopExisting(ctx, home)
	}
	held := LockHeld(home)
	if !held {
		if spawn == nil {
			spawn = SpawnDetached
		}
		if _, err := spawn(ctx, home, lifecycle, port); err != nil {
			return Status{}, false, fmt.Errorf("starting web companion: %w", err)
		}
	}
	status, err := WaitReady(ctx, home, 8*time.Second)
	if err != nil {
		return Status{}, false, err
	}
	status.Started = !held
	return status, !held, nil
}

// WaitReady polls Probe until the companion answers or the timeout passes.
func WaitReady(ctx context.Context, home string, timeout time.Duration) (Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		if status, _ := Probe(ctx, home); status.Running {
			return status, nil
		}
		if time.Now().After(deadline) {
			return Status{}, fmt.Errorf("web companion did not become ready (log: %s)", LogPath(home))
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// controlRequest issues a token-authed control call against the companion.
func controlRequest(ctx context.Context, home, method, path string, payload any) ([]byte, error) {
	bridge, err := backend.ReadBridgeFile(home)
	if err != nil {
		return nil, errors.New("web companion is not running")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(bridge.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("web companion is not running")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bridge.Token) != "" {
		request.Header.Set("X-Membox-Token", bridge.Token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, errors.New("web companion is not running")
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("companion returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

// Stop asks the companion to shut down gracefully and waits for it to exit.
func Stop(ctx context.Context, home string) error {
	if status, _ := Probe(ctx, home); !status.Running {
		return errors.New("web companion is not running")
	}
	if _, err := controlRequest(ctx, home, http.MethodPost, "/api/companion/stop", nil); err != nil {
		return err
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		if status, _ := Probe(ctx, home); !status.Running && !LockHeld(home) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("web companion did not stop in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}
