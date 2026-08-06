package backend

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// Companion lifecycle modes. "session" companions follow their parent process
// (the TUI stops them on quit); "keep" companions stay running until the user
// explicitly stops them.
const (
	CompanionLifecycleSession = "session"
	CompanionLifecycleKeep    = "keep"
)

// tabPresence tracks one browser tab's heartbeat so the TUI can show how many
// readers are connected and whether any of them hold unsaved notes.
type tabPresence struct {
	lastSeen time.Time
	dirty    bool
}

// tabRetention is how long a silent tab stays visible before it is pruned.
const tabRetention = 35 * time.Second

// CompanionState aggregates the control-plane facts the status endpoint
// reports: lifecycle mode, start time, and connected browser tabs.
type CompanionState struct {
	PID       int       `json:"pid"`
	Mode      string    `json:"mode"`
	StartedAt time.Time `json:"started_at"`
	Tabs      int       `json:"tabs"`
	DirtyTabs int       `json:"dirty_tabs"`
	Port      int       `json:"port"`
	BaseURL   string    `json:"base_url"`
}

// ConfigureCompanion marks the server as a managed Web Companion and wires the
// control endpoints. onStop is invoked (once) when a stop request arrives;
// onLifecycle is invoked when the lifecycle mode changes at runtime.
func (s *Server) ConfigureCompanion(mode string, onStop func(), onLifecycle func(mode string)) {
	s.companionMu.Lock()
	defer s.companionMu.Unlock()
	s.companionMode = mode
	s.companionStartedAt = time.Now()
	s.companionTabs = make(map[string]tabPresence)
	s.onCompanionStop = onStop
	s.onCompanionLifecycle = onLifecycle
}

// CompanionState returns a snapshot for status reporting. The mode is empty
// when the server is not running as a managed companion (tests, dev servers).
func (s *Server) CompanionState() CompanionState {
	s.companionMu.Lock()
	defer s.companionMu.Unlock()
	tabs, dirty := s.pruneTabsLocked()
	port := 0
	if s.baseURL != "" {
		port = portFromBaseURL(s.baseURL)
	}
	return CompanionState{
		PID:       os.Getpid(),
		Mode:      s.companionMode,
		StartedAt: s.companionStartedAt,
		Tabs:      tabs,
		DirtyTabs: dirty,
		Port:      port,
		BaseURL:   s.baseURL,
	}
}

func portFromBaseURL(baseURL string) int {
	index := strings.LastIndex(baseURL, ":")
	if index < 0 {
		return 0
	}
	port := 0
	for _, char := range baseURL[index+1:] {
		if char < '0' || char > '9' {
			return 0
		}
		port = port*10 + int(char-'0')
	}
	return port
}

// pruneTabsLocked drops stale heartbeats and returns live totals.
func (s *Server) pruneTabsLocked() (tabs, dirty int) {
	cutoff := time.Now().Add(-tabRetention)
	for id, presence := range s.companionTabs {
		if presence.lastSeen.Before(cutoff) {
			delete(s.companionTabs, id)
			continue
		}
		tabs++
		if presence.dirty {
			dirty++
		}
	}
	return tabs, dirty
}

// handleCompanionPresence receives browser-tab heartbeats. Same-origin reader
// pages carry no bridge token, so this endpoint stays open like /api/save.
// Parameters: tab (stable per-tab id), dirty ("1" when unsaved notes exist),
// gone ("1" on pagehide so the tab disappears immediately).
func (s *Server) handleCompanionPresence(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := request.URL.Query()
	tab := strings.TrimSpace(query.Get("tab"))
	if tab == "" || len(tab) > 64 {
		http.Error(writer, "tab id is required", http.StatusBadRequest)
		return
	}
	s.companionMu.Lock()
	if s.companionTabs == nil {
		s.companionTabs = make(map[string]tabPresence)
	}
	if query.Get("gone") == "1" {
		delete(s.companionTabs, tab)
	} else {
		s.companionTabs[tab] = tabPresence{lastSeen: time.Now(), dirty: query.Get("dirty") == "1"}
	}
	tabs, dirty := s.pruneTabsLocked()
	s.companionMu.Unlock()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"tabs": tabs, "dirty_tabs": dirty})
}

// handleCompanionStatus reports the companion control plane to the TUI/CLI.
func (s *Server) handleCompanionStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	state := s.CompanionState()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"running":    true,
		"pid":        state.PID,
		"mode":       state.Mode,
		"started_at": state.StartedAt.UTC().Format(time.RFC3339),
		"tabs":       state.Tabs,
		"dirty_tabs": state.DirtyTabs,
		"port":       state.Port,
		"base_url":   state.BaseURL,
	})
}

// handleCompanionStop triggers a graceful companion shutdown. The HTTP
// response is sent before the process actually exits.
func (s *Server) handleCompanionStop(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	s.companionMu.Lock()
	onStop := s.onCompanionStop
	s.companionMu.Unlock()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"stopping": true})
	if onStop != nil {
		go onStop()
	}
}

// handleCompanionLifecycle switches the lifecycle mode of a running companion
// (session → keep stops the parent watcher; keep → session re-arms it).
func (s *Server) handleCompanionLifecycle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	var payload struct {
		Mode string `json:"mode"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	mode := strings.TrimSpace(payload.Mode)
	if mode != CompanionLifecycleSession && mode != CompanionLifecycleKeep {
		http.Error(writer, "mode must be session or keep", http.StatusBadRequest)
		return
	}
	s.companionMu.Lock()
	changed := s.companionMode != mode
	s.companionMode = mode
	onLifecycle := s.onCompanionLifecycle
	s.companionMu.Unlock()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"mode": mode})
	if changed && onLifecycle != nil {
		onLifecycle(mode)
	}
}
