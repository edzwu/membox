//go:build windows

package companion

import "syscall"

// parentAlive opens a query handle to the parent process; a dead PID fails
// the open.
func parentAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const queryLimited = 0x1000
	handle, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(handle)
	return true
}
