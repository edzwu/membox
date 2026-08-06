//go:build !windows

package system

import (
	"os"
	"path/filepath"
	"syscall"
)

// OSMutex is a cross-process mutex. On Unix it is an exclusive flock on a
// lock file inside dir; the file is never deleted (deletion races concurrent
// openers). Blocking Lock matches the mutation-serialization contract: a
// writer waits for the current owner instead of corrupting shared state.
type OSMutex struct {
	file *os.File
}

// OpenOSMutex prepares the named cross-process lock inside dir.
func OpenOSMutex(dir, name string) (*OSMutex, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &OSMutex{file: file}, nil
}

// Lock blocks until the lock is held.
func (m *OSMutex) Lock() error {
	return syscall.Flock(int(m.file.Fd()), syscall.LOCK_EX)
}

// TryLock reports whether the lock could be taken immediately; a true result
// leaves the lock held and must be followed by Unlock.
func (m *OSMutex) TryLock() (bool, error) {
	err := syscall.Flock(int(m.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return false, nil
	}
	return false, err
}

// Unlock releases the lock.
func (m *OSMutex) Unlock() error {
	return syscall.Flock(int(m.file.Fd()), syscall.LOCK_UN)
}

// Close releases the underlying file handle.
func (m *OSMutex) Close() error {
	return m.file.Close()
}
