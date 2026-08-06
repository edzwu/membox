//go:build !windows

package companion

import (
	"os/exec"
	"syscall"

	"membox/internal/infrastructure/system"
)

// AcquireLock atomically takes the per-home companion mutex.
func AcquireLock(home string) (func(), error) {
	lock, err := system.OpenOSMutex(home, "companion")
	if err != nil {
		return nil, err
	}
	held, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !held {
		_ = lock.Close()
		return nil, ErrAlreadyRunning
	}
	return func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}, nil
}

// LockHeld reports whether another process currently owns the mutex.
func LockHeld(home string) bool {
	lock, err := system.OpenOSMutex(home, "companion")
	if err != nil {
		return false
	}
	defer lock.Close()
	held, err := lock.TryLock()
	if err != nil || !held {
		return true
	}
	_ = lock.Unlock()
	return false
}

// detach puts the child in its own session so terminal closure does not send
// SIGHUP to a keep-mode companion.
func detach(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setsid = true
}
