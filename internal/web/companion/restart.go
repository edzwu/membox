package companion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"membox/internal/web/backend"
)

// Restart stops any companion belonging to this home (and any stale listener on
// the preferred extension port), then starts a fresh one. The TUI calls this on
// entry so bridge.json, port 8787, and the Agent manager always match the new
// session instead of reusing an orphaned or ephemeral server.
func Restart(ctx context.Context, home, lifecycle string, port int, spawn SpawnFunc) (Status, error) {
	if lifecycle == "" {
		lifecycle = LifecycleKeep
	}
	_ = stopExisting(ctx, home)

	// Wait briefly for the singleton lock and preferred port to free.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := Probe(ctx, home); status.Running || LockHeld(home) {
			select {
			case <-ctx.Done():
				return Status{}, ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
			continue
		}
		break
	}
	// Last resort: free the extension default port when a legacy/orphan mm is
	// still bound there without answering companion control endpoints.
	if port == 0 || port == DefaultPort {
		defaultURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultPort)
		if bridgeAlive(ctx, defaultURL) {
			if _, ok := probeCompanionURL(ctx, defaultURL, ""); !ok {
				// Prefer tokenized probe; empty token still detects unauthenticated legacy.
				forceFreePort(DefaultPort)
			}
		} else if listenerBusy(DefaultPort) {
			// Port held but HTTP not answering — still free leftover mm listeners.
			forceFreePort(DefaultPort)
		}
	}

	if spawn == nil {
		spawn = SpawnDetached
	}
	if _, err := spawn(ctx, home, lifecycle, port); err != nil {
		return Status{}, fmt.Errorf("starting web companion: %w", err)
	}
	status, err := WaitReady(ctx, home, 10*time.Second)
	if err != nil {
		return Status{}, err
	}
	status.Started = true
	return status, nil
}

// stopExisting best-effort stops every companion we can address for this home:
// the live bridge.json target first, then the preferred fixed port.
func stopExisting(ctx context.Context, home string) error {
	var last error
	if status, _ := Probe(ctx, home); status.Running {
		if err := Stop(ctx, home); err != nil {
			last = err
		}
	}
	// Also stop a companion answering on the default extension port even when
	// bridge.json points elsewhere (split-brain after ephemeral fallbacks).
	token := ""
	if bridge, err := backend.ReadBridgeFile(home); err == nil {
		token = bridge.Token
	}
	if token == "" {
		if t, err := backend.LoadOrCreateBridgeToken(home); err == nil {
			token = t
		}
	}
	defaultURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultPort)
	if status, ok := probeCompanionURL(ctx, defaultURL, token); ok && status.Running {
		if err := stopCompanionURL(ctx, defaultURL, token); err != nil {
			last = err
		} else {
			// Wait until it is gone.
			waitDeadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(waitDeadline) {
				if _, still := probeCompanionURL(ctx, defaultURL, token); !still {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(150 * time.Millisecond):
				}
			}
		}
	}
	return last
}

func probeCompanionURL(ctx context.Context, baseURL, token string) (Status, bool) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return Status{}, false
	}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/api/companion/status", nil)
	if err != nil {
		return Status{}, false
	}
	if strings.TrimSpace(token) != "" {
		request.Header.Set("X-Membox-Token", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Status{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Status{}, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Status{}, false
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
		return Status{}, false
	}
	startedAt, _ := time.Parse(time.RFC3339, payload.StartedAt)
	return Status{
		Running:   true,
		BaseURL:   baseURL,
		Port:      payload.Port,
		Token:     token,
		Mode:      payload.Mode,
		PID:       payload.PID,
		Tabs:      payload.Tabs,
		DirtyTabs: payload.DirtyTabs,
		StartedAt: startedAt.Local(),
	}, true
}

func stopCompanionURL(ctx context.Context, baseURL, token string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, baseURL+"/api/companion/stop", nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		request.Header.Set("X-Membox-Token", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("companion stop returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// bridgeAlive reports whether a membox bridge answers on baseURL (legacy
// servers without the companion control plane still implement this).
func bridgeAlive(ctx context.Context, baseURL string) bool {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	requestCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/api/bridge/status", nil)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		Connected bool `json:"connected"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload)
	return payload.Connected
}
