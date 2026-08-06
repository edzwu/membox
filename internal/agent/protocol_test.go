package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestJSONLReaderSplitsOnLFOnly(t *testing.T) {
	// U+2028 and U+2029 must stay inside the frame.
	payload := `{"type":"message","text":"line\u2028still-same"}` + "\n" + `{"type":"next"}` + "\n"
	r := newJSONLReader(strings.NewReader(payload), DefaultMaxFrameBytes)
	frame1, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(frame1, []byte("\\u2028")) && !bytes.Contains(frame1, []byte("\u2028")) {
		// JSON may keep the escape or decoded form depending on source; ensure single frame held both parts.
		if !strings.Contains(string(frame1), "still-same") {
			t.Fatalf("frame split on unicode separator: %q", frame1)
		}
	}
	if strings.Contains(string(frame1), `"type":"next"`) {
		t.Fatalf("first frame consumed second record: %q", frame1)
	}
	frame2, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(frame2) != `{"type":"next"}` {
		t.Fatalf("second frame = %q", frame2)
	}
}

func TestJSONLReaderStripsCRLF(t *testing.T) {
	r := newJSONLReader(strings.NewReader("{\"type\":\"a\"}\r\n{\"type\":\"b\"}\n"), 0)
	f1, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(f1) != `{"type":"a"}` {
		t.Fatalf("got %q", f1)
	}
	f2, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if string(f2) != `{"type":"b"}` {
		t.Fatalf("got %q", f2)
	}
}

func TestJSONLReaderRejectsOversizedFrame(t *testing.T) {
	big := strings.Repeat("x", 100)
	r := newJSONLReader(strings.NewReader(big), 50)
	_, err := r.ReadFrame()
	if err == nil {
		t.Fatal("expected oversized error")
	}
	if CodeOf(err) != CodeProtocolError {
		t.Fatalf("code = %s", CodeOf(err))
	}
}

func TestJSONLReaderPartialThenComplete(t *testing.T) {
	pr, pw := io.Pipe()
	r := newJSONLReader(pr, 0)
	done := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		frame, err := r.ReadFrame()
		if err != nil {
			errCh <- err
			return
		}
		done <- frame
	}()
	_, _ = pw.Write([]byte(`{"type":"par`))
	_, _ = pw.Write([]byte(`tial"}` + "\n"))
	_ = pw.Close()
	select {
	case err := <-errCh:
		t.Fatal(err)
	case frame := <-done:
		if string(frame) != `{"type":"partial"}` {
			t.Fatalf("got %q", frame)
		}
	}
}

func TestJSONLWriterSerializesLF(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONLWriter(&buf)
	if err := w.WriteJSON(map[string]any{"type": "prompt", "id": "1"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("missing LF: %q", out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["type"] != "prompt" {
		t.Fatalf("obj = %#v", obj)
	}
}

func TestEventStreamReplayAndReset(t *testing.T) {
	s := newEventStream("sess-1")
	e1 := s.Publish(Event{Type: EventAssistantDelta, Payload: payloadObject(map[string]any{"text": "a"})})
	_ = s.Publish(Event{Type: EventAssistantDelta, Payload: payloadObject(map[string]any{"text": "b"})})

	ctx := t.Context()
	sub, err := s.Subscribe(ctx, e1.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ev := <-sub.Events()
	if ev.Type != EventAssistantDelta {
		t.Fatalf("type = %s", ev.Type)
	}
	var payload map[string]any
	_ = json.Unmarshal(ev.Payload, &payload)
	if payload["text"] != "b" {
		t.Fatalf("payload = %#v", payload)
	}

	// Unknown epoch forces reset.
	sub2, err := s.Subscribe(ctx, "deadbeef:1")
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	reset := <-sub2.Events()
	if reset.Type != EventStreamReset {
		t.Fatalf("expected reset, got %s", reset.Type)
	}
}

func TestParseEventID(t *testing.T) {
	epoch, seq, ok := parseEventID("abc12def:42")
	if !ok || epoch != "abc12def" || seq != 42 {
		t.Fatalf("got %s %d %v", epoch, seq, ok)
	}
	if _, _, ok := parseEventID("noseq"); ok {
		t.Fatal("expected failure")
	}
}
