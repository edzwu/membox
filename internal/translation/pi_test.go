package translation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiRunnerStreamsIsolatedQwenTranslation(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	promptPath := filepath.Join(dir, "prompt")
	script := filepath.Join(dir, "fake-pi")
	body := `#!/bin/sh
printf '%s\n' "$*" > "$FAKE_PI_ARGS"
IFS= read -r prompt
printf '%s\n' "$prompt" > "$FAKE_PI_PROMPT"
printf '%s\n' '{"id":"request","type":"response","command":"prompt","success":true}'
printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"你好"}}'
printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"，世界。"}}'
printf '%s\n' '{"type":"agent_settled"}'
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PI_ARGS", argsPath)
	t.Setenv("FAKE_PI_PROMPT", promptPath)

	var events []Event
	err := (PiRunner{PiPath: script, Home: dir, ExtensionPath: "/tmp/membox-ollama.ts"}).Stream(context.Background(), Request{
		ID: "p-1", Title: "Article", Text: "Hello, world.",
	}, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Type != "start" || events[1].Text+events[2].Text != "你好，世界。" || events[3].Type != "done" {
		t.Fatalf("events=%+v", events)
	}
	if events[0].Provider != DefaultProvider || events[0].Model != DefaultModel {
		t.Fatalf("start event=%+v", events[0])
	}
	args, _ := os.ReadFile(argsPath)
	for _, expected := range []string{"--mode rpc", "--no-session", "--no-builtin-tools", "--no-extensions", "--extension /tmp/membox-ollama.ts", "--provider membox-ollama", "--model qwen3:14b", "--thinking off"} {
		if !strings.Contains(string(args), expected) {
			t.Fatalf("Pi args missing %q: %s", expected, args)
		}
	}
	prompt, _ := os.ReadFile(promptPath)
	if !strings.Contains(string(prompt), "Simplified Chinese") || !strings.Contains(string(prompt), `Hello, world.`) {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}

func TestMaterializeProviderPinsNativeOllamaStreaming(t *testing.T) {
	path, err := MaterializeProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`registerProvider("membox-ollama"`, `/api/chat`, `think: false`, `qwen3:14b`,
		`keep_alive`, `KEEP_ALIVE`, `num_ctx: 8192`,
	} {
		if !strings.Contains(string(body), marker) {
			t.Fatalf("provider extension missing %q", marker)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("provider mode=%v err=%v", info.Mode(), err)
	}
}

func TestValidateRequestRejectsEmptyAndOversizedSegment(t *testing.T) {
	if _, err := validateRequest(Request{ID: "x"}); err == nil {
		t.Fatal("empty text accepted")
	}
	if _, err := validateRequest(Request{ID: "x", Text: strings.Repeat("x", MaxSegmentBytes+1)}); err == nil {
		t.Fatal("oversized segment accepted")
	}
}
