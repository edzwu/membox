package companion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"membox/internal/web/backend"
)

func TestAcquireLockBlocksSecondAcquire(t *testing.T) {
	home := t.TempDir()
	release, err := AcquireLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if !LockHeld(home) {
		t.Fatal("lock should read as held while acquired")
	}
	if _, err := AcquireLock(home); err != ErrAlreadyRunning {
		t.Fatalf("second acquire = %v, want ErrAlreadyRunning", err)
	}
	release()
	if LockHeld(home) {
		t.Fatal("lock should be free after release")
	}
	if _, err := AcquireLock(home); err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
}

func writeBridge(t *testing.T, home, baseURL, token string) {
	t.Helper()
	if _, err := backend.WriteBridgeFile(home, backend.BridgeFile{BaseURL: baseURL, Token: token, Port: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestProbeParsesCompanionStatus(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/companion/status" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("X-Membox-Token") != "secret" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"running": true, "pid": 4242, "mode": "keep",
			"started_at": time.Now().UTC().Format(time.RFC3339),
			"tabs":       2, "dirty_tabs": 1, "port": 8787,
		})
	}))
	defer fake.Close()

	home := t.TempDir()
	writeBridge(t, home, fake.URL, "secret")

	status, err := Probe(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.PID != 4242 || status.Mode != "keep" || status.Tabs != 2 || status.DirtyTabs != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.BaseURL != fake.URL || status.Token != "secret" {
		t.Fatalf("bridge fields not carried through: %+v", status)
	}
}

func TestProbeStaleBridgeReportsNotRunning(t *testing.T) {
	home := t.TempDir()
	// Nothing listens on this address: a dead companion must read as stopped.
	writeBridge(t, home, "http://127.0.0.1:1", "token")
	status, err := Probe(context.Background(), home)
	if err != nil {
		t.Fatalf("probe must not error on stale bridge.json: %v", err)
	}
	if status.Running {
		t.Fatal("stale bridge.json must not read as running")
	}
}

func TestProbeMissingBridgeReportsNotRunning(t *testing.T) {
	status, err := Probe(context.Background(), t.TempDir())
	if err != nil || status.Running {
		t.Fatalf("probe on empty home = %+v, %v", status, err)
	}
}

func TestEnsureConnectsToRunningWithoutSpawning(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"running": true, "pid": 7, "mode": "session", "port": 8787})
	}))
	defer fake.Close()

	home := t.TempDir()
	writeBridge(t, home, fake.URL, "")

	spawned := false
	status, didSpawn, err := Ensure(context.Background(), home, LifecycleSession, 0, func(context.Context, string, string, int, int) (int, error) {
		spawned = true
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if spawned || didSpawn {
		t.Fatal("Ensure must not spawn when a companion answers")
	}
	if !status.Running || status.Started {
		t.Fatalf("unexpected ensure result: %+v", status)
	}
}

func TestStopPostsStopAndWaitsForExit(t *testing.T) {
	home := t.TempDir()
	var stopCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/companion/status":
			if stopCount.Load() > 0 {
				// After the stop request the companion is gone: refuse connections
				// the way a closed listener would.
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{"running": true, "pid": os.Getpid()})
		case "/api/companion/stop":
			stopCount.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"stopping":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	writeBridge(t, home, server.URL, "")

	if err := Stop(context.Background(), home); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if stopCount.Load() != 1 {
		t.Fatalf("stop endpoint called %d times, want 1", stopCount.Load())
	}
}

func TestStopOnIdleHomeReportsNotRunning(t *testing.T) {
	if err := Stop(context.Background(), t.TempDir()); err == nil {
		t.Fatal("stop on empty home should report not running")
	}
}

func TestSetLifecycleValidatesMode(t *testing.T) {
	if err := SetLifecycle(context.Background(), t.TempDir(), "forever"); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
}

func TestRunServesStatusAndStopsOnRequest(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, Options{
			Home:      home,
			Port:      0, // ephemeral: never fight a developer's real :8787
			Lifecycle: LifecycleKeep,
			Version:   "test",
			OnReady:   func(baseURL, _ string) { ready <- baseURL },
		})
	}()

	var baseURL string
	select {
	case baseURL = <-ready:
	case err := <-runErr:
		t.Fatalf("companion exited before ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("companion did not become ready")
	}

	status, err := Probe(ctx, home)
	if err != nil || !status.Running || status.Mode != LifecycleKeep || status.PID != os.Getpid() {
		t.Fatalf("probe after run = %+v, %v", status, err)
	}

	// Presence heartbeats aggregate tabs and dirty counts.
	for _, call := range []string{"/api/companion/presence?tab=a&dirty=1", "/api/companion/presence?tab=b&dirty=0"} {
		resp, err := http.Get(baseURL + call)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	status, _ = Probe(ctx, home)
	if status.Tabs != 2 || status.DirtyTabs != 1 {
		t.Fatalf("presence not aggregated: %+v", status)
	}

	// bridge.json carries the companion identity for the extension.
	bridge, err := backend.ReadBridgeFile(home)
	if err != nil || bridge.Mode != LifecycleKeep || bridge.PID != os.Getpid() {
		t.Fatalf("bridge file missing companion identity: %+v, %v", bridge, err)
	}

	if err := Stop(ctx, home); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("companion did not exit after stop")
	}
	if LockHeld(home) {
		t.Fatal("lock must be released after the companion exits")
	}
}

func TestRunRefusesSecondInstance(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Home: home, Port: 0, Lifecycle: LifecycleKeep, Version: "test",
			OnReady: func(string, string) { ready <- struct{}{} },
		})
	}()
	select {
	case <-ready:
	case <-time.After(15 * time.Second):
		t.Fatal("first companion did not start")
	}

	err := Run(context.Background(), Options{Home: home, Port: 0, Lifecycle: LifecycleKeep, Version: "test"})
	if err == nil {
		t.Fatal("second companion must refuse to start")
	}

	_ = Stop(ctx, home)
	<-done
}

// TestHelperSleepProcess is not a test: invoked as a child binary it sleeps
// so another test can kill it and observe parent-death handling.
func TestHelperSleepProcess(t *testing.T) {
	if os.Getenv("MEMBOX_HELPER_SLEEP") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
}

func sleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestHelperSleepProcess")
	command.Env = append(os.Environ(), "MEMBOX_HELPER_SLEEP=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return command
}

func TestRunSessionModeExitsWhenParentDies(t *testing.T) {
	// Spawn a sleeper process to act as the parent, then run a session
	// companion watching it and kill the parent. Reap the corpse promptly:
	// an unreaped zombie still answers signal 0, while real parents get
	// reaped by their own parent (shell, launchd).
	home := t.TempDir()
	sleeper := sleepProcess(t)
	defer func() {
		_ = sleeper.Process.Kill()
		_, _ = sleeper.Process.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Home: home, Port: 0, Lifecycle: LifecycleSession, ParentPID: sleeper.Process.Pid, Version: "test",
			OnReady: func(string, string) { ready <- struct{}{} },
		})
	}()
	select {
	case <-ready:
	case <-time.After(15 * time.Second):
		t.Fatal("session companion did not start")
	}

	if err := sleeper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := sleeper.Process.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		// Companion observed the parent's death and shut down on its own.
	case <-time.After(10 * time.Second):
		t.Fatal("session companion did not exit after its parent died")
	}
}
