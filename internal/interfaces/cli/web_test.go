package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLI_WebStatusReportsNotRunningOnIdleHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, _ := runTestCLI(t, "--home", home, "web", "status")
	if code != 0 {
		t.Fatalf("status on idle home should succeed, got code %d", code)
	}
	if !strings.Contains(stdout, "not running") {
		t.Fatalf("status should report not running: %q", stdout)
	}
}

func TestCLI_WebStopReportsNotRunningOnIdleHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, _ := runTestCLI(t, "--home", home, "web", "stop")
	if code != 0 {
		t.Fatalf("stop on idle home should succeed, got code %d", code)
	}
	if !strings.Contains(stdout, "not running") {
		t.Fatalf("stop should report not running: %q", stdout)
	}
}

func TestCLI_WebRequiresSubcommand(t *testing.T) {
	code, _, stderr := runTestCLI(t, "web")
	if code != 2 {
		t.Fatalf("bare web should print usage, got code %d", code)
	}
	if !strings.Contains(stderr, "web command is required") {
		t.Fatalf("missing guidance: %s", stderr)
	}
}

// TestCLI_WebLifecycle runs a real companion in-process (web run), then
// exercises status/open discovery/stop across separate CLI invocations.
func TestCLI_WebLifecycle(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan int, 1)
	go func() {
		// Ephemeral port so the test never fights a developer's real :8787.
		runDone <- Run(ctx, []string{"--home", home, "web", "run", "--lifecycle", "keep", "--port", "0"},
			strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, fakeLauncher{}, false)
	}()

	bridgePath := filepath.Join(home, "bridge.json")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(bridgePath); err == nil {
			code, stdout, _ := runTestCLI(t, "--home", home, "web", "status")
			if code == 0 && strings.Contains(stdout, "running") && strings.Contains(stdout, "http://127.0.0.1:") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("companion did not become reachable")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A second run must refuse while the companion holds the lock.
	code, _, stderr := runTestCLI(t, "--home", home, "web", "run", "--lifecycle", "keep", "--port", "0")
	if code == 0 || !strings.Contains(stderr, "already running") {
		t.Fatalf("second run must refuse: code=%d stderr=%q", code, stderr)
	}

	// Stop from a fresh CLI invocation, then the companion exits.
	code, stdout, _ := runTestCLI(t, "--home", home, "web", "stop")
	if code != 0 || !strings.Contains(stdout, "stopped") {
		t.Fatalf("stop failed: code=%d stdout=%q", code, stdout)
	}
	select {
	case runCode := <-runDone:
		if runCode != 0 && runCode != 130 {
			t.Fatalf("web run exited with %d", runCode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("companion did not exit after stop")
	}

	// Status is quiet again.
	code, stdout, _ = runTestCLI(t, "--home", home, "web", "status")
	if code != 0 || !strings.Contains(stdout, "not running") {
		t.Fatalf("status after stop = %q (code=%d)", stdout, code)
	}
}
