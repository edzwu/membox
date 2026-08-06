//go:build windows

package system

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"
)

// OSMutex is a cross-process mutex backed by a Windows named mutex. The
// name is derived from a hash of dir+name because mutex names may not
// contain path separators. Pidfiles were rejected: check-then-write is not
// atomic, and Signal-based liveness probes are unreliable on Windows.
type OSMutex struct {
	handle syscall.Handle
}

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	createMutexW  = kernel32.NewProc("CreateMutexW")
	releaseMutex  = kernel32.NewProc("ReleaseMutex")
	waitForSingle = kernel32.NewProc("WaitForSingleObject")
)

const (
	waitObject0        = 0
	waitAbandoned      = 0x00000080
	waitTimeout        = 258
	infiniteWaitMillis = 0xFFFFFFFF
)

func osMutexName(dir, name string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir) + "\x00" + name))
	return "Local\\membox-" + hex.EncodeToString(sum[:])[:32]
}

// OpenOSMutex prepares the named cross-process lock. CreateMutexW returns the
// existing object when another process created it first, so all processes
// contend on one kernel object.
func OpenOSMutex(dir, name string) (*OSMutex, error) {
	namePtr, err := syscall.UTF16PtrFromString(osMutexName(dir, name))
	if err != nil {
		return nil, err
	}
	handle, _, callErr := createMutexW.Call(uintptr(0), uintptr(0), uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return nil, errors.New("creating mutex: " + callErr.Error())
	}
	return &OSMutex{handle: syscall.Handle(handle)}, nil
}

// Lock blocks until the lock is held.
func (m *OSMutex) Lock() error {
	result, _, callErr := waitForSingle.Call(uintptr(m.handle), uintptr(infiniteWaitMillis))
	if result == waitObject0 || result == waitAbandoned {
		return nil
	}
	return errors.New("acquiring mutex: " + callErr.Error())
}

// TryLock reports whether the lock could be taken immediately; a true result
// leaves the lock held and must be followed by Unlock.
func (m *OSMutex) TryLock() (bool, error) {
	result, _, callErr := waitForSingle.Call(uintptr(m.handle), uintptr(0))
	switch result {
	case waitObject0, waitAbandoned:
		return true, nil
	case waitTimeout:
		return false, nil
	default:
		return false, errors.New("probing mutex: " + callErr.Error())
	}
}

// Unlock releases the lock.
func (m *OSMutex) Unlock() error {
	ok, _, callErr := releaseMutex.Call(uintptr(m.handle))
	if ok == 0 {
		return errors.New("releasing mutex: " + callErr.Error())
	}
	return nil
}

// Close releases the underlying handle.
func (m *OSMutex) Close() error {
	return syscall.CloseHandle(m.handle)
}
