package system

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"sync"
)

// MutationLock serializes document mutations across processes (TUI, CLI, and
// the Web Companion all open the same database and Markdown tree) and is
// reentrant within one goroutine so composed operations such as
// SaveAnnotationNote → CreateNote take the lock exactly once for the whole
// resolve → file write → observe → commit sequence.
type MutationLock struct {
	process sync.Mutex
	os      *OSMutex
	ownerMu sync.Mutex
	owner   int64
	depth   int
}

// OpenMutationLock prepares the mutation lock rooted at dir.
func OpenMutationLock(dir string) (*MutationLock, error) {
	osMutex, err := OpenOSMutex(dir, "mutation")
	if err != nil {
		return nil, err
	}
	return &MutationLock{os: osMutex}, nil
}

// goroutineID identifies the calling goroutine for reentrancy tracking. It is
// parsed from the runtime stack because the runtime does not expose the ID
// directly; the format "goroutine N [" is a long-standing stable convention.
func goroutineID() int64 {
	var buf [64]byte
	stack := buf[:runtime.Stack(buf[:], false)]
	stack = bytes.TrimPrefix(stack, []byte("goroutine "))
	if index := bytes.IndexByte(stack, ' '); index > 0 {
		if id, err := strconv.ParseInt(string(stack[:index]), 10, 64); err == nil {
			return id
		}
	}
	panic("membox: cannot determine goroutine identity for the mutation lock")
}

// Lock takes the mutation lock, reentering when the same goroutine already
// holds it.
func (l *MutationLock) Lock() error {
	id := goroutineID()
	l.ownerMu.Lock()
	if l.owner == id && l.depth > 0 {
		l.depth++
		l.ownerMu.Unlock()
		return nil
	}
	l.ownerMu.Unlock()

	l.process.Lock()
	if err := l.os.Lock(); err != nil {
		l.process.Unlock()
		return fmt.Errorf("acquiring mutation lock: %w", err)
	}
	l.ownerMu.Lock()
	l.owner, l.depth = id, 1
	l.ownerMu.Unlock()
	return nil
}

// Unlock releases one level of the mutation lock.
func (l *MutationLock) Unlock() error {
	id := goroutineID()
	l.ownerMu.Lock()
	if l.owner != id || l.depth == 0 {
		l.ownerMu.Unlock()
		return fmt.Errorf("mutation lock released without being held")
	}
	l.depth--
	if l.depth > 0 {
		l.ownerMu.Unlock()
		return nil
	}
	l.owner = 0
	l.ownerMu.Unlock()

	osErr := l.os.Unlock()
	l.process.Unlock()
	return osErr
}

// Close releases the underlying OS resource.
func (l *MutationLock) Close() error {
	return l.os.Close()
}

// WithLock runs fn under the mutation lock.
func WithLock[T any](l *MutationLock, fn func() (T, error)) (T, error) {
	if l == nil {
		return fn()
	}
	if err := l.Lock(); err != nil {
		var zero T
		return zero, err
	}
	defer func() { _ = l.Unlock() }()
	return fn()
}

// DoLock runs fn under the mutation lock.
func DoLock(l *MutationLock, fn func() error) error {
	_, err := WithLock(l, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}
