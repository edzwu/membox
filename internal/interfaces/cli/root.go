package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"membox"
	"membox/internal/interfaces/host"
	"membox/internal/interfaces/tui"
)

type runtime struct {
	home     string
	once     sync.Once
	box      *membox.Box
	launcher host.Launcher
	err      error
}

func (r *runtime) get() (*membox.Box, error) {
	r.once.Do(func() {
		config, err := membox.DefaultConfig()
		if err != nil {
			r.err = err
			return
		}
		if r.home != "" {
			config.Home, config.DatabasePath = r.home, ""
		}
		r.box, r.err = membox.Open(config)
	})
	return r.box, r.err
}

func (r *runtime) close() error {
	if r.box != nil {
		return r.box.Close()
	}
	return nil
}

type usageError struct {
	command *cobra.Command
	err     error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usageErr(command *cobra.Command, message string) error {
	return &usageError{command: command, err: errors.New(message)}
}

func Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer, launcher host.Launcher, isTTY bool) int {
	runtime := &runtime{launcher: launcher}
	root := newRoot(ctx, runtime, in, out, stderr, isTTY)
	root.SetArgs(args)
	command, err := root.ExecuteC()
	closeErr := runtime.close()
	if err == nil && closeErr != nil {
		err = closeErr
	}
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "Error: %v\n", err)
	var use *usageError
	if errors.As(err, &use) {
		fmt.Fprintln(stderr)
		use.command.SetOut(stderr)
		_ = use.command.Usage()
		return 2
	}
	if isUnknownCommand(err) {
		nearest := deepestCommand(root, args)
		fmt.Fprintln(stderr)
		nearest.SetOut(stderr)
		_ = nearest.Usage()
		return 2
	}
	_ = command
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

func newRoot(ctx context.Context, runtime *runtime, in io.Reader, out, stderr io.Writer, isTTY bool) *cobra.Command {
	root := &cobra.Command{
		Use:           "mm",
		Short:         "Local document identity for Markdown",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErr(cmd, fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY {
				return usageErr(cmd, "a command is required when not running in a terminal")
			}
			box, err := runtime.get()
			if err != nil {
				return err
			}
			return tui.Run(ctx, box, runtime.launcher, tea.WithInput(in), tea.WithOutput(out))
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Version = membox.Version
	configureFlagErrors(root)
	root.SetContext(ctx)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&runtime.home, "home", "", "membox home directory")
	root.AddCommand(
		newPathCommand(runtime),
		newDocCommand(runtime),
		newNoteCommand(runtime),
		newTopicCommand(runtime),
		newLinkCommand(runtime),
		newTrashCommand(runtime),
		newIndexCommand(runtime),
		newServeCommand(runtime),
		newWebCommand(runtime),
		newAgentCommand(runtime),
	)
	root.SetVersionTemplate("{{printf \"%s\" .Version}}\n")
	return root
}

func parentCommand(use, short, missing string) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short}
	command.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usageErr(cmd, fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()))
		}
		return nil
	}
	command.RunE = func(cmd *cobra.Command, _ []string) error { return usageErr(cmd, missing) }
	return command
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErr(cmd, fmt.Sprintf("unexpected argument %q", args[0]))
	}
	return nil
}

func configureFlagErrors(command *cobra.Command) {
	command.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return &usageError{command: cmd, err: err} })
	for _, child := range command.Commands() {
		configureFlagErrors(child)
	}
}

func exactArgs(count int, names string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != count {
			if len(args) < count {
				return usageErr(cmd, "missing "+names)
			}
			return usageErr(cmd, fmt.Sprintf("expected %d argument(s), received %d", count, len(args)))
		}
		return nil
	}
}

func maxArgs(count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > count {
			return usageErr(cmd, fmt.Sprintf("accepts at most %d argument(s), received %d", count, len(args)))
		}
		return nil
	}
}

func isUnknownCommand(err error) bool { return stringsContains(err.Error(), "unknown command") }
func stringsContains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func deepestCommand(root *cobra.Command, args []string) *cobra.Command {
	current := root
	for _, arg := range args {
		if len(arg) > 0 && arg[0] == '-' {
			continue
		}
		found := false
		for _, child := range current.Commands() {
			if child.Name() == arg || child.HasAlias(arg) {
				current, found = child, true
				break
			}
		}
		if !found {
			break
		}
	}
	return current
}
