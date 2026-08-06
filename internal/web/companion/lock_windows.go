//go:build windows

package companion

import (
	"os/exec"
	"syscall"

	"membox/internal/infrastructure/system"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// AcquireLock atomically takes a Windows named mutex. Unlike a pidfile this
// has no check/write race and is released by the kernel after a crash.
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

// detach prevents Ctrl+C or console teardown in the parent from terminating a
// keep-mode companion.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
		HideWindow:    true,
	}
}
