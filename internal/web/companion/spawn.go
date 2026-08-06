package companion

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// SpawnDetached starts `mm web run` as a detached child so it survives the
// TUI exiting (keep mode) and does not receive terminal signals (Setsid). The
// child's output goes to companion.log for post-mortem diagnostics.
func SpawnDetached(ctx context.Context, home, lifecycle string, port, parentPID int) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locating membox binary: %w", err)
	}
	command := exec.CommandContext(ctx, executable,
		"--home", home,
		"web", "run",
		"--lifecycle", lifecycle,
		"--port", strconv.Itoa(port),
		"--parent", strconv.Itoa(parentPID),
	)
	detach(command)
	logFile, err := os.OpenFile(LogPath(home), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("opening companion log: %w", err)
	}
	defer logFile.Close()
	command.Stdout, command.Stderr = logFile, logFile
	command.Stdin = nil
	if err := command.Start(); err != nil {
		return 0, err
	}
	// The companion manages its own lifetime; do not wait for it here.
	go func() { _ = command.Wait() }()
	return command.Process.Pid, nil
}
