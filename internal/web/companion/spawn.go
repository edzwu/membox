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
func SpawnDetached(ctx context.Context, home, _ string, port int) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locating membox binary: %w", err)
	}
	command, err := newDetachedCommand(ctx, executable,
		"--home", home,
		"web", "run",
		"--port", strconv.Itoa(port),
	)
	if err != nil {
		return 0, err
	}
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

func newDetachedCommand(ctx context.Context, executable string, arguments ...string) (*exec.Cmd, error) {
	// A detached keep companion must not inherit cancellation from the TUI or
	// CLI command that launched it. Check cancellation before Start, then let
	// the companion own its lifecycle.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command(executable, arguments...)
	detach(command)
	return command, nil
}
