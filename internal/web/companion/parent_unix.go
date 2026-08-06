//go:build !windows

package companion

import "syscall"

// parentAlive probes the parent with signal 0. Orphaned children get
// reparented to launchd/init, but the original PID disappears either way.
func parentAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
