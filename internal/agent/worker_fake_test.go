package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakePiScript is a tiny RPC peer used for protocol contract tests.
const fakePiScript = `#!/usr/bin/env bash
set -euo pipefail
# Emit get_state / prompt responses correlated by id. Stream a short assistant turn.
while IFS= read -r line || [[ -n "$line" ]]; do
  # strip CR
  line="${line%$'\r'}"
  id=$(printf '%s' "$line" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
  typ=$(printf '%s' "$line" | sed -n 's/.*"type"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
  case "$typ" in
    get_state)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"pi-sess-1\",\"sessionFile\":\"$MEMBOX_FAKE_SESSION\",\"thinkingLevel\":\"medium\",\"isStreaming\":false,\"messageCount\":0,\"model\":{\"id\":\"fake\",\"provider\":\"test\",\"name\":\"Fake\"}}}"
      ;;
    get_messages)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_messages\",\"success\":true,\"data\":{\"messages\":[]}}"
      ;;
    get_available_models)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"id\":\"fake\",\"provider\":\"test\",\"name\":\"Fake\"}]}}"
      ;;
    prompt)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true}"
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_start","contentIndex":0}}'
      printf '%s\n' '{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello"}}'
      printf '%s\n' '{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":" world"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}]}}'
      printf '%s\n' '{"type":"agent_end","messages":[],"willRetry":false}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    abort)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"abort\",\"success\":true}"
      ;;
    *)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"$typ\",\"success\":false,\"error\":\"unknown\"}"
      ;;
  esac
done
`

func writeFakePi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pi")
	if runtime.GOOS == "windows" {
		t.Skip("fake pi shell script is unix-only")
	}
	if err := os.WriteFile(path, []byte(fakePiScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Ensure bash exists.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required for fake pi")
	}
	return path
}

type memCatalog struct {
	recs map[string]SessionRecord
}

func (m *memCatalog) ListSessions(ctx context.Context, includeArchived bool) ([]SessionRecord, error) {
	out := make([]SessionRecord, 0, len(m.recs))
	for _, r := range m.recs {
		if !includeArchived && r.Archived {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
func (m *memCatalog) GetSession(ctx context.Context, id string) (SessionRecord, bool, error) {
	r, ok := m.recs[id]
	return r, ok, nil
}
func (m *memCatalog) InsertSession(ctx context.Context, rec SessionRecord) error {
	if m.recs == nil {
		m.recs = map[string]SessionRecord{}
	}
	m.recs[rec.ID] = rec
	return nil
}
func (m *memCatalog) UpdateSession(ctx context.Context, rec SessionRecord) error {
	m.recs[rec.ID] = rec
	return nil
}
func (m *memCatalog) TouchSession(ctx context.Context, id string, at time.Time) error {
	r := m.recs[id]
	r.LastUsedAt = at
	m.recs[id] = r
	return nil
}
func (m *memCatalog) ArchiveSession(ctx context.Context, id string, at time.Time) error {
	r := m.recs[id]
	r.Archived = true
	r.UpdatedAt = at
	m.recs[id] = r
	return nil
}

func TestWorkerPromptStreamsNormalizedEvents(t *testing.T) {
	fake := writeFakePi(t)
	home := t.TempDir()
	sessionDir := filepath.Join(home, "agent", "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "test.jsonl")
	// Pre-create so get_state path is valid if needed.
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMBOX_FAKE_SESSION", sessionFile)

	stream := newEventStream("membox-sess")
	w := newWorker(context.Background(), workerConfig{
		PiPath:        fake,
		SessionDir:    sessionDir,
		ExtensionPath: filepath.Join(home, "missing.ts"), // not loaded by fake
		Tools:         ToolNamesReadOnly(),
		Home:          home,
		SessionID:     "membox-sess",
		WorkerID:      "w1",
	}, stream)
	// Fake ignores extension path; Start still runs get_state.
	// Point PATH-style executable:
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = w.Stop(context.Background()) }()

	sub, err := stream.Subscribe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := w.prompt(context.Background(), "run-1", "client-1", "hi", ""); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	deadline := time.After(5 * time.Second)
	var sawDelta, sawSettled bool
	for !sawSettled {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for events")
		case ev := <-sub.Events():
			switch ev.Type {
			case EventAssistantDelta:
				sawDelta = true
				var p map[string]any
				_ = json.Unmarshal(ev.Payload, &p)
				if p["text"] == nil {
					t.Fatalf("delta missing text: %s", ev.Payload)
				}
			case EventRunSettled:
				sawSettled = true
			}
		}
	}
	if !sawDelta {
		t.Fatal("expected assistant.delta")
	}
	if w.IsBusy() {
		t.Fatal("worker should be idle after settle")
	}
}

func TestManagerCreateAndPromptIdempotent(t *testing.T) {
	fake := writeFakePi(t)
	home := t.TempDir()
	sessionDir := filepath.Join(home, "agent", "sessions")
	_ = os.MkdirAll(sessionDir, 0o700)
	sessionFile := filepath.Join(sessionDir, "created.jsonl")
	_ = os.WriteFile(sessionFile, []byte("{}\n"), 0o600)
	t.Setenv("MEMBOX_FAKE_SESSION", sessionFile)

	// Make fake-pi appear as "pi" for probe? Manager uses configured PiPath.
	cat := &memCatalog{recs: map[string]SessionRecord{}}
	mgr := NewManager(Config{
		Home:            home,
		Enabled:         true,
		PiPath:          fake,
		MaxWorkers:      2,
		IdleTimeout:     time.Hour,
		Catalog:         cat,
		ExtensionSource: []byte("// fake\nexport default function() {}\n"),
	}).(*manager)

	// Bypass version probe path by pre-setting:
	// ensureProbed runs pi --version; fake script doesn't handle that.
	// Wrap with a version-capable script.
	versioned := filepath.Join(t.TempDir(), "pi")
	script := fmt.Sprintf(`#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "0.9.0-fake"; exit 0; fi
export MEMBOX_FAKE_SESSION=%q
exec %q "$@"
`, sessionFile, fake)
	if err := os.WriteFile(versioned, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr.cfg.PiPath = versioned

	ctx := context.Background()
	snap, err := mgr.CreateSession(ctx, CreateSessionCommand{Title: "t", ClientID: "c1", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if snap.Session.ID == "" {
		t.Fatal("missing session id")
	}
	// Idempotent create
	snap2, err := mgr.CreateSession(ctx, CreateSessionCommand{Title: "t", ClientID: "c1", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Session.ID != snap.Session.ID {
		t.Fatalf("idempotency broke: %s vs %s", snap.Session.ID, snap2.Session.ID)
	}

	run, err := mgr.Prompt(ctx, PromptCommand{
		SessionID: snap.Session.ID, ClientID: "c1", IdempotencyKey: "p1", Text: "hello",
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	run2, err := mgr.Prompt(ctx, PromptCommand{
		SessionID: snap.Session.ID, ClientID: "c1", IdempotencyKey: "p1", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != run2.RunID {
		t.Fatalf("prompt idempotency failed")
	}

	// Busy rejection with different key while still running is racy; wait settle then ensure second prompt works.
	time.Sleep(200 * time.Millisecond)
	_ = mgr.Shutdown(ctx)
}
