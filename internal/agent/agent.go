package agent

import (
	"log"
	"time"
)

// Start runs a placeholder Spoke agent.
// In the full MVP this will read backends.toml, watch local directories
// with fsnotify, maintain local SQLite FTS, and push changes to Hub.
func Start(hubURL string) error {
	log.Printf("[agent] connecting to hub at %s", hubURL)
	// TODO: establish WebSocket connection, register backends, start fsnotify watchers
	for {
		time.Sleep(30 * time.Second)
		log.Println("[agent] heartbeat (placeholder)")
	}
}
