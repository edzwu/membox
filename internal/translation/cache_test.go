package translation

import (
	"context"
	"testing"
)

func TestCacheRoundTripAndKeyIsolation(t *testing.T) {
	cache, err := OpenCache(CachePath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	ctx := context.Background()
	request := Request{ID: "p-1", Text: "Hello,   world."}

	key, normalized := Key(request, DefaultProvider, DefaultModel)
	if normalized != "Hello, world." {
		t.Fatalf("normalized=%q", normalized)
	}
	if _, ok, err := cache.Get(ctx, key); err != nil || ok {
		t.Fatalf("unexpected cache hit: ok=%v err=%v", ok, err)
	}
	if err := cache.Store(ctx, key, request, DefaultProvider, DefaultModel, "你好，世界。"); err != nil {
		t.Fatal(err)
	}
	hit, ok, err := cache.Get(ctx, key)
	if err != nil || !ok || hit != "你好，世界。" {
		t.Fatalf("hit=%q ok=%v err=%v", hit, ok, err)
	}

	// Whitespace-only source differences share one key.
	otherKey, _ := Key(Request{ID: "p-9", Text: "  Hello, world. "}, DefaultProvider, DefaultModel)
	if otherKey != key {
		t.Fatal("whitespace variants produced different keys")
	}
	// A different model or target language must never replay this output.
	for _, variant := range [][3]string{
		{DefaultProvider, "qwen3:8b", DefaultTargetLanguage},
		{DefaultProvider, DefaultModel, "Japanese"},
	} {
		variantKey, _ := Key(request, variant[0], variant[1])
		if variant[2] != DefaultTargetLanguage {
			variantKey, _ = Key(Request{ID: "p-1", Text: request.Text, TargetLanguage: variant[2]}, variant[0], variant[1])
		}
		if _, ok, _ := cache.Get(ctx, variantKey); ok {
			t.Fatalf("cross-model/target hit for %v", variant)
		}
	}
	if err := cache.Store(ctx, key, request, DefaultProvider, DefaultModel, "  "); err == nil {
		t.Fatal("empty translation was cached")
	}
}

type countingStreamer struct {
	calls int
	text  string
}

func (c *countingStreamer) Stream(_ context.Context, request Request, emit EmitFunc) error {
	c.calls++
	for _, event := range []Event{
		{Type: "start", ID: request.ID},
		{Type: "delta", ID: request.ID, Text: c.text},
		{Type: "done", ID: request.ID},
	} {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func TestCachedStreamerReplaysWithoutCallingModel(t *testing.T) {
	cache, err := OpenCache(CachePath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	inner := &countingStreamer{text: "缓存译文"}
	streamer := CachedStreamer{Cache: cache, Inner: inner, Provider: DefaultProvider, Model: DefaultModel}

	var events []Event
	emit := func(event Event) error { events = append(events, event); return nil }
	request := Request{ID: "p-1", Text: "Cache me."}
	if err := streamer.Stream(context.Background(), request, emit); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("first call did not reach inner streamer: %d", inner.calls)
	}
	events = nil
	if err := streamer.Stream(context.Background(), request, emit); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("cache hit still called the model: %d", inner.calls)
	}
	if len(events) != 3 || events[0].Type != "start" || events[1].Type != "delta" || events[1].Text != "缓存译文" || events[2].Type != "done" {
		t.Fatalf("replayed events=%+v", events)
	}
	if events[0].Provider != DefaultProvider || events[0].Model != DefaultModel {
		t.Fatalf("replayed start event lost identity: %+v", events[0])
	}
}

func TestCachedStreamerDoesNotCacheFailedRuns(t *testing.T) {
	cache, err := OpenCache(CachePath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	failing := CachedStreamer{Cache: cache, Inner: failingStreamer{}, Provider: DefaultProvider, Model: DefaultModel}
	err = failing.Stream(context.Background(), Request{ID: "x", Text: "never cached"}, func(Event) error { return nil })
	if err == nil {
		t.Fatal("expected inner failure")
	}
	var count int
	if err := cache.db.QueryRow(`SELECT COUNT(*) FROM translation_cache`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed run was cached: count=%d err=%v", count, err)
	}
}

type failingStreamer struct{}

func (failingStreamer) Stream(context.Context, Request, EmitFunc) error {
	return context.DeadlineExceeded
}

// cancelStreamer simulates a model that is interrupted mid-paragraph: one
// partial delta went out, then the client disconnected.
type cancelStreamer struct{ started chan struct{} }

func (c cancelStreamer) Stream(ctx context.Context, request Request, emit EmitFunc) error {
	if err := emit(Event{Type: "start", ID: request.ID}); err != nil {
		return err
	}
	if err := emit(Event{Type: "delta", ID: request.ID, Text: "半截译文"}); err != nil {
		return err
	}
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestCachedStreamerNeverCachesInterruptedParagraph(t *testing.T) {
	cache, err := OpenCache(CachePath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	inner := cancelStreamer{started: make(chan struct{})}
	streamer := CachedStreamer{Cache: cache, Inner: inner, Provider: DefaultProvider, Model: DefaultModel}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- streamer.Stream(ctx, Request{ID: "p-1", Text: "Interrupted mid-stream."}, func(Event) error { return nil })
	}()
	<-inner.started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("interrupted stream reported success")
	}
	var count int
	if err := cache.db.QueryRow(`SELECT COUNT(*) FROM translation_cache`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial translation was cached: count=%d err=%v", count, err)
	}

	// The next attempt translates from scratch rather than replaying a stub.
	inner2 := &countingStreamer{text: "完整译文"}
	streamer.Inner = inner2
	if err := streamer.Stream(context.Background(), Request{ID: "p-1", Text: "Interrupted mid-stream."}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if inner2.calls != 1 {
		t.Fatalf("interrupted paragraph was not retranslated: calls=%d", inner2.calls)
	}
	if err := streamer.Stream(context.Background(), Request{ID: "p-1", Text: "Interrupted mid-stream."}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if inner2.calls != 1 {
		t.Fatalf("completed paragraph was not replayed from cache: calls=%d", inner2.calls)
	}
}
