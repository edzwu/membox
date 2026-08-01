package host

import (
	"context"
	"os/exec"
)

// Launcher is a presentation-host port used by CLI and TUI adapters.
type Launcher interface {
	EditorCommand(context.Context, string) (*exec.Cmd, error)
	ViewerCommand(context.Context, string) (*exec.Cmd, error)
	OpenCommand(context.Context, string) (*exec.Cmd, error)
}
