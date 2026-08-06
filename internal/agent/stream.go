package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultRingEvents = 1000
	defaultRingBytes  = 2 << 20 // 2 MiB
	defaultSubQueue   = 64
)

// eventStream is a per-session normalized event ring with live subscribers.
type eventStream struct {
	mu         sync.Mutex
	sessionID  string
	epoch      string
	seq        uint64
	events     []Event
	bytes      int
	maxEvents  int
	maxBytes   int
	subQueue   int
	subs       map[uint64]*subscriber
	nextSubID  uint64
	closed     bool
}

type subscriber struct {
	id     uint64
	ch     chan Event
	cancel context.CancelFunc
}

func newEventStream(sessionID string) *eventStream {
	return &eventStream{
		sessionID: sessionID,
		epoch:     newEpoch(),
		maxEvents: defaultRingEvents,
		maxBytes:  defaultRingBytes,
		subQueue:  defaultSubQueue,
		subs:      make(map[uint64]*subscriber),
	}
}

func newEpoch() string {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Short prefix keeps event IDs readable: <epoch8>:<seq>
	return id.String()[:8]
}

func (s *eventStream) Epoch() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

func (s *eventStream) LastEventID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return ""
	}
	return s.events[len(s.events)-1].ID
}

func (s *eventStream) Reset(reason string) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch = newEpoch()
	s.seq = 0
	s.events = nil
	s.bytes = 0
	payload, _ := json.Marshal(map[string]any{"reason": reason})
	ev := s.appendLocked(Event{
		Version:   1,
		SessionID: s.sessionID,
		Type:      EventStreamReset,
		At:        time.Now().UTC(),
		Payload:   payload,
	})
	return ev
}

func (s *eventStream) Publish(ev Event) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ev
	}
	ev.Version = 1
	if ev.SessionID == "" {
		ev.SessionID = s.sessionID
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if ev.Payload == nil {
		ev.Payload = json.RawMessage(`{}`)
	}
	return s.appendLocked(ev)
}

func (s *eventStream) appendLocked(ev Event) Event {
	s.seq++
	ev.ID = fmt.Sprintf("%s:%d", s.epoch, s.seq)
	// Approximate size: id + type + payload.
	size := len(ev.ID) + len(ev.Type) + len(ev.Payload) + 64
	s.events = append(s.events, ev)
	s.bytes += size
	for (len(s.events) > s.maxEvents || s.bytes > s.maxBytes) && len(s.events) > 1 {
		dropped := s.events[0]
		dropSize := len(dropped.ID) + len(dropped.Type) + len(dropped.Payload) + 64
		s.bytes -= dropSize
		s.events = s.events[1:]
	}
	// Fan-out. Slow subscribers are disconnected (non-blocking send).
	for id, sub := range s.subs {
		select {
		case sub.ch <- ev:
		default:
			close(sub.ch)
			delete(s.subs, id)
			if sub.cancel != nil {
				sub.cancel()
			}
		}
	}
	return ev
}

// Subscribe replays events after afterEventID (exclusive), then streams live events.
// If afterEventID is empty, only live events are delivered (snapshot already has history).
// If the cursor is unknown/evicted or epoch mismatches, a stream.reset is emitted first.
func (s *eventStream) Subscribe(ctx context.Context, afterEventID string) (Subscription, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmtError(CodeUnavailable, "event stream closed")
	}
	subCtx, cancel := context.WithCancel(ctx)
	id := atomic.AddUint64(&s.nextSubID, 1)
	sub := &subscriber{
		id:     id,
		ch:     make(chan Event, s.subQueue),
		cancel: cancel,
	}
	s.subs[id] = sub

	var replay []Event
	needReset := false
	if afterEventID != "" {
		epoch, seq, ok := parseEventID(afterEventID)
		if !ok || epoch != s.epoch {
			needReset = true
		} else {
			found := false
			for i, ev := range s.events {
				_, evSeq, _ := parseEventID(ev.ID)
				if evSeq == seq {
					found = true
					replay = append(replay, s.events[i+1:]...)
					break
				}
			}
			if !found {
				// Cursor may refer to an event still "current" if ring starts after it
				// but epoch matches and seq < first retained — treat as reset.
				if len(s.events) == 0 {
					// Empty ring with matching epoch: nothing to replay.
				} else {
					_, firstSeq, _ := parseEventID(s.events[0].ID)
					if seq < firstSeq {
						needReset = true
					}
					// If seq > last, nothing to replay (client is caught up).
				}
			}
		}
	}
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			if existing, ok := s.subs[id]; ok && existing == sub {
				delete(s.subs, id)
				close(sub.ch)
			}
			s.mu.Unlock()
			cancel()
		}()
		if needReset {
			payload, _ := json.Marshal(map[string]any{"reason": "replay_unavailable"})
			reset := Event{
				Version:   1,
				SessionID: s.sessionID,
				Type:      EventStreamReset,
				At:        time.Now().UTC(),
				Payload:   payload,
			}
			// Synthetic id so clients can still advance.
			s.mu.Lock()
			reset = s.appendLocked(reset)
			// appendLocked already fan-out; remove from replay path.
			s.mu.Unlock()
			_ = reset
		} else {
			for _, ev := range replay {
				select {
				case <-subCtx.Done():
					return
				case sub.ch <- ev:
				}
			}
		}
		<-subCtx.Done()
	}()

	return &streamSubscription{sub: sub, parent: s, ctx: subCtx}, nil
}

func (s *eventStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, sub := range s.subs {
		close(sub.ch)
		if sub.cancel != nil {
			sub.cancel()
		}
		delete(s.subs, id)
	}
}

func (s *eventStream) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

type streamSubscription struct {
	sub    *subscriber
	parent *eventStream
	ctx    context.Context
}

func (s *streamSubscription) Events() <-chan Event { return s.sub.ch }

func (s *streamSubscription) Close() error {
	s.sub.cancel()
	return nil
}

func parseEventID(id string) (epoch string, seq uint64, ok bool) {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			epoch = id[:i]
			var n uint64
			for _, c := range id[i+1:] {
				if c < '0' || c > '9' {
					return "", 0, false
				}
				n = n*10 + uint64(c-'0')
			}
			if epoch == "" {
				return "", 0, false
			}
			return epoch, n, true
		}
	}
	return "", 0, false
}

func payloadObject(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
