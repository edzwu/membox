package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// DefaultMaxFrameBytes is the default maximum size of one JSONL frame.
const DefaultMaxFrameBytes = 8 << 20 // 8 MiB

// rpcCommand is a Pi RPC stdin command envelope.
type rpcCommand map[string]any

// rpcResponse is a Pi RPC response object.
type rpcResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// rpcEvent is a loosely typed Pi RPC stdout event.
type rpcEvent map[string]json.RawMessage

func (e rpcEvent) Type() string {
	var t string
	_ = json.Unmarshal(e["type"], &t)
	return t
}

func (e rpcEvent) String(key string) string {
	raw, ok := e[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

func (e rpcEvent) Bool(key string) bool {
	raw, ok := e[key]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	return false
}

func (e rpcEvent) Raw(key string) json.RawMessage {
	return e[key]
}

// jsonlWriter serializes writes of one JSON object per LF-terminated line.
type jsonlWriter struct {
	w   io.Writer
	mu  sync.Mutex
	buf []byte
}

func newJSONLWriter(w io.Writer) *jsonlWriter {
	return &jsonlWriter{w: w, buf: make([]byte, 0, 4096)}
}

func (j *jsonlWriter) WriteJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", CodeProtocolError, err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = j.buf[:0]
	j.buf = append(j.buf, payload...)
	j.buf = append(j.buf, '\n')
	written := 0
	for written < len(j.buf) {
		n, writeErr := j.w.Write(j.buf[written:])
		written += n
		if writeErr != nil {
			return fmt.Errorf("%s: write: %w", CodeProtocolError, writeErr)
		}
	}
	return nil
}

// jsonlReader splits an input stream on byte 0x0A only.
// It deliberately does not treat U+2028/U+2029 as record boundaries.
type jsonlReader struct {
	r        io.Reader
	maxFrame int
	buf      []byte
	pending  []byte
	closed   atomic.Bool
}

func newJSONLReader(r io.Reader, maxFrame int) *jsonlReader {
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrameBytes
	}
	return &jsonlReader{
		r:        r,
		maxFrame: maxFrame,
		buf:      make([]byte, 32*1024),
	}
}

// ReadFrame returns the next LF-delimited frame with an optional trailing CR stripped.
func (j *jsonlReader) ReadFrame() ([]byte, error) {
	for {
		if idx := bytes.IndexByte(j.pending, '\n'); idx >= 0 {
			line := j.pending[:idx]
			j.pending = j.pending[idx+1:]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			// Copy so callers can retain the frame while the reader reuses pending.
			out := make([]byte, len(line))
			copy(out, line)
			return out, nil
		}
		if j.closed.Load() {
			if len(j.pending) == 0 {
				return nil, io.EOF
			}
			// Incomplete final frame without LF is a protocol error.
			return nil, fmtError(CodeProtocolError, "incomplete JSONL frame at EOF")
		}
		n, err := j.r.Read(j.buf)
		if n > 0 {
			if len(j.pending)+n > j.maxFrame {
				j.closed.Store(true)
				return nil, fmtError(CodeProtocolError, "JSONL frame exceeds %d bytes", j.maxFrame)
			}
			j.pending = append(j.pending, j.buf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				j.closed.Store(true)
				continue
			}
			return nil, err
		}
	}
}

// decodeRPCEvent parses one stdout frame into a loosely typed event map.
func decodeRPCEvent(frame []byte) (rpcEvent, error) {
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 {
		return nil, fmtError(CodeProtocolError, "empty JSONL frame")
	}
	var event rpcEvent
	if err := json.Unmarshal(frame, &event); err != nil {
		return nil, wrapError(CodeProtocolError, "invalid JSONL frame", err)
	}
	if event.Type() == "" {
		return nil, fmtError(CodeProtocolError, "JSONL frame missing type")
	}
	return event, nil
}

// ringLog is a bounded stderr capture that never mixes into the protocol stream.
type ringLog struct {
	mu       sync.Mutex
	lines    []string
	maxLines int
	maxBytes int
	bytes    int
}

func newRingLog(maxLines, maxBytes int) *ringLog {
	if maxLines <= 0 {
		maxLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	return &ringLog{maxLines: maxLines, maxBytes: maxBytes}
}

func (r *ringLog) Write(p []byte) (int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(p))
	// Allow long stderr lines without failing the scanner.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	r.mu.Lock()
	defer r.mu.Unlock()
	for scanner.Scan() {
		line := scanner.Text()
		r.lines = append(r.lines, line)
		r.bytes += len(line) + 1
		for len(r.lines) > r.maxLines || r.bytes > r.maxBytes {
			r.bytes -= len(r.lines[0]) + 1
			r.lines = r.lines[1:]
		}
	}
	return len(p), nil
}

func (r *ringLog) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	copy(out, r.lines[len(r.lines)-n:])
	return out
}
