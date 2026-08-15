package translation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// ProbeSocket reports whether the mmd daemon answers health checks on its
// owner-only Unix socket.
func ProbeSocket(socketPath string) error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}},
	}
	response, err := client.Get("http://mmd/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("mmd health HTTP %d", response.StatusCode)
	}
	return nil
}

// EnsureMMD verifies that mmd is running for the given home and spawns a
// detached `mmd run` when it is not. The spawned daemon outlives the caller
// on purpose: it is the shared local-model gateway for every client.
// Concurrent callers are safe — mmd's singleton lock rejects duplicate starts
// and the health wait accepts whichever instance becomes ready.
func EnsureMMD(ctx context.Context, home string) error {
	socket := SocketPath(home)
	if ProbeSocket(socket) == nil {
		return nil
	}
	binaryPath, err := findMMDBinary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(home, "mmd", "mmd-stdout.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(binaryPath, "--home", home, "run")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// Detach into its own session: the daemon must survive this process, and
	// its Pi children stay in its process group for clean shutdown.
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning mmd: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(8 * time.Second)
	for {
		if ProbeSocket(socket) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mmd did not become ready at %s", socket)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// findMMDBinary locates the mmd executable: first next to the running binary
// (`bin/mm` → `bin/mmd`), then on PATH.
func findMMDBinary() (string, error) {
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "mmd")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("mmd"); err == nil {
		return path, nil
	}
	return "", errors.New("mmd binary not found next to the current executable or on PATH")
}
