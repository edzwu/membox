//go:build !windows

package companion

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// listenerBusy reports that something is accepting TCP on port.
func listenerBusy(port int) bool { return listenerPID(port) > 0 }

// forceFreePort terminates a leftover membox listener on port when graceful
// companion stop is unavailable (legacy foreground companion without control endpoints).
// Non-membox processes are left alone.
func forceFreePort(port int) {
	if port <= 0 {
		return
	}
	pid := listenerPID(port)
	if pid <= 0 {
		return
	}
	if !isMemboxProcess(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if listenerPID(port) != pid {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if listenerPID(port) != pid {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func listenerPID(port int) int {
	// lsof is available on macOS and most Linux desktops where membox runs.
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(bytes.TrimSpace(out)))
	if len(fields) == 0 {
		return 0
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func isMemboxProcess(pid int) bool {
	// ps -p <pid> -o command=  → path containing /mm or ending with mm
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	cmd := strings.TrimSpace(string(out))
	if cmd == "" {
		return false
	}
	base := cmd
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		base = cmd[:i]
	}
	if strings.HasSuffix(base, "/mm") || base == "mm" || strings.HasSuffix(base, "\\mm.exe") {
		return true
	}
	// Detached child: "mm web run …"; foreground: "mm web start --fg" / legacy "mm serve"
	if strings.Contains(cmd, " web run") ||
		strings.Contains(cmd, " web start") ||
		strings.Contains(cmd, " serve") {
		return strings.Contains(base, "mm")
	}
	return false
}
