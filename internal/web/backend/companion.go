package backend

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// Companion lifecycle mode. The companion is a long-lived keep daemon stopped
// only by an explicit /stop request (mm web stop / TUI quit with web_on_exit=stop).
// Session/lease lifecycle was removed: it made Miru/clipper/assist fragile
// whenever the TUI released leases or reused a stale process.
const (
	CompanionLifecycleKeep = "keep"
)

// tabPresence tracks one browser tab's heartbeat so the TUI can show how many
// readers are connected and whether any of them hold unsaved notes.
type tabPresence struct {
	lastSeen time.Time
	dirty    bool
}

// tabRetention is how long a silent tab stays visible before it is pruned.
const (
	tabRetention       = 35 * time.Second
	maxCompanionTabs   = 256
	presenceHeaderName = "X-Membox-Presence"
)

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
// stop control endpoint. onStop is invoked (once) when a stop request arrives.
func (s *Server) ConfigureCompanion(onStop func()) {
	s.companionMu.Lock()
	defer s.companionMu.Unlock()
	s.companionMode = CompanionLifecycleKeep
	s.companionStartedAt = time.Now()
	s.companionTabs = make(map[string]tabPresence)
	s.onCompanionStop = onStop
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

// handleCompanionPresence receives same-origin browser-tab heartbeats. POST
// plus a custom header prevents image/form CSRF; Sec-Fetch-Site rejects an
// explicit cross-site request. The bounded map prevents untrusted tab IDs from
// growing companion memory without limit.
func (s *Server) handleCompanionPresence(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(presenceHeaderName) != "1" || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(writer, "forbidden", http.StatusForbidden)
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
		s.pruneTabsLocked()
		if _, exists := s.companionTabs[tab]; !exists && len(s.companionTabs) >= maxCompanionTabs {
			oldestID := ""
			var oldest time.Time
			for id, presence := range s.companionTabs {
				if oldestID == "" || presence.lastSeen.Before(oldest) {
					oldestID, oldest = id, presence.lastSeen
				}
			}
			delete(s.companionTabs, oldestID)
		}
		s.companionTabs[tab] = tabPresence{lastSeen: time.Now(), dirty: query.Get("dirty") == "1"}
	}
	tabs, dirty := s.pruneTabsLocked()
	s.companionMu.Unlock()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(map[string]any{"tabs": tabs, "dirty_tabs": dirty})
}

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
