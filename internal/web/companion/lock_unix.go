//go:build !windows

package companion

import (
	"os"
	"os/exec"
	"syscall"
)

// AcquireLock takes the per-home singleton lock with a non-blocking flock.
// The returned release function unlocks and closes the file; the lock file
// itself stays on disk (deleting it would race a concurrent acquirer).
func AcquireLock(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(LockPath(home), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, ErrAlreadyRunning
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// detach puts the child in its own session so closing the TUI's terminal does
// not deliver SIGHUP to the companion.
func detach(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setsid = true
}

// LockHeld reports whether another process currently holds the singleton lock.
func LockHeld(home string) bool {
	file, err := os.OpenFile(LockPath(home), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}
