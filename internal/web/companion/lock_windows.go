//go:build windows

package companion

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// On Windows the singleton is a pidfile plus a process-liveness check: flock
// is unavailable, and exclusive file handles would break crash recovery.
func readLockPID(home string) int {
	body, err := os.ReadFile(LockPath(home))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(body)))
	return pid
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes existence without affecting the target.
	return process.Signal(os.Signal(nil)) == nil
}

// AcquireLock claims the pidfile when no live companion owns it.
func AcquireLock(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if LockHeld(home) {
		return nil, ErrAlreadyRunning
	}
	if err := os.WriteFile(LockPath(home), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return nil, err
	}
	return func() {
		if readLockPID(home) == os.Getpid() {
			_ = os.Remove(LockPath(home))
		}
	}, nil
}

// LockHeld reports whether a live process owns the pidfile.
func LockHeld(home string) bool {
	return processAlive(readLockPID(home))
}

// detach is a no-op on Windows: child processes already survive the parent's
// console closing.
func detach(*exec.Cmd) {}
