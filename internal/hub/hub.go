package hub

import (
	"fmt"
	"log"
	"net/http"
)

// Start runs a placeholder Hub HTTP+WebSocket server.
// In the full MVP this will include SQLite FTS, backend routing table,
// and WebSocket management for Spoke connections.
func Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// TODO: upgrade to WebSocket and register Spoke
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"ws not yet implemented"}`))
	})
	addr := fmt.Sprintf(":%d", port)
	log.Printf("[hub] listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
