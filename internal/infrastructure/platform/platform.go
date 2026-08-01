package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/google/shlex"
)

type Launcher struct{}

func (Launcher) EditorCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	parts, err := shlex.Split(editor)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, errors.New("editor command is empty")
	}
	return exec.CommandContext(ctx, parts[0], append(parts[1:], path)...), nil
}

func (Launcher) ViewerCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	if command := strings.TrimSpace(os.Getenv("MEMBOX_VIEWER")); command != "" {
		parts, err := shlex.Split(command)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			return nil, errors.New("viewer command is empty")
		}
		return exec.CommandContext(ctx, parts[0], append(parts[1:], path)...), nil
	}
	return exec.CommandContext(ctx, "leaf", path), nil
}

func (Launcher) OpenCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", path), nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return exec.CommandContext(ctx, "xdg-open", path), nil
	case "windows":
		return exec.CommandContext(ctx, "cmd", "/c", "start", "", path), nil
	default:
		return nil, errors.New("opening files is not supported on this platform")
	}
}
