package translation

import (
	"context"
	"path/filepath"
)

const (
	DefaultProvider       = "ollama"
	DefaultModel          = "qwen3:14b"
	DefaultTargetLanguage = "Simplified Chinese (zh-CN)"
	MaxSegmentBytes       = 32 << 10

	// Stream modes share one NDJSON endpoint; the prompt branches on Mode.
	ModeTranslate = "translate" // default plain bilingual translation
	ModeJPStudy   = "jp-study"  // furigana + chunking + grammar + zh-CN
)

// SocketPath is the single source of truth for the mmd translation socket
// location, shared by the daemon listener and the Web Companion client.
func SocketPath(home string) string {
	return filepath.Join(home, "mmd", "mmd.sock")
}

type Request struct {
	ID             string `json:"id"`
	Title          string `json:"title,omitempty"`
	TargetLanguage string `json:"target_language,omitempty"`
	// Mode selects the prompt template. Empty means ModeTranslate.
	Mode string `json:"mode,omitempty"`
	Text string `json:"text"`
}

// Event is the NDJSON unit streamed mmd → Companion → Miru.
type Event struct {
	Type     string `json:"type"` // start | delta | done | error
	ID       string `json:"id"`
	Text     string `json:"text,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type EmitFunc func(Event) error

type Streamer interface {
	Stream(ctx context.Context, request Request, emit EmitFunc) error
}

// Completer is a one-shot local-LLM prompt through Pi. The PDF structure
// planner uses it; translation uses the streaming variant above.
type Completer interface {
	Complete(ctx context.Context, prompt string) (string, error)
}
