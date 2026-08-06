package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"membox"
	"membox/internal/web/companion"
)

// newWebCommand groups the Web Companion controls. The companion is the one
// process per membox home that owns the reader, the browser bridge, and
// web-originated writes; these commands probe, start, stop, and open it
// without needing the TUI.
func newWebCommand(runtime *runtime) *cobra.Command {
	command := parentCommand("web", "Control the membox Web Companion", "web command is required: status, start, stop, open")
	command.AddCommand(
		newWebStatusCommand(runtime),
		newWebStartCommand(runtime),
		newWebStopCommand(runtime),
		newWebOpenCommand(runtime),
		newWebRunCommand(runtime),
	)
	return command
}

func resolveHome(runtime *runtime) (string, error) {
	if runtime.home != "" {
		return runtime.home, nil
	}
	config, err := membox.DefaultConfig()
	if err != nil {
		return "", err
	}
	return config.Home, nil
}

func newWebStatusCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the Web Companion state",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			status, _ := companion.Probe(cmd.Context(), home)
			out := cmd.OutOrStdout()
			if !status.Running {
				fmt.Fprintln(out, "web companion is not running")
				fmt.Fprintln(out, "Start it with: mm web start")
				return nil
			}
			fmt.Fprintf(out, "web companion is running\n")
			fmt.Fprintf(out, "  URL:      %s\n", status.BaseURL)
			fmt.Fprintf(out, "  Mode:     %s\n", status.Mode)
			fmt.Fprintf(out, "  PID:      %d\n", status.PID)
			fmt.Fprintf(out, "  Started:  %s\n", formatUptime(status.StartedAt))
			fmt.Fprintf(out, "  Tabs:     %d connected", status.Tabs)
			if status.DirtyTabs > 0 {
				fmt.Fprintf(out, " · %d unsaved", status.DirtyTabs)
			}
			fmt.Fprintln(out)
			return nil
		},
	}
}

func formatUptime(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "unknown"
	}
	elapsed := time.Since(startedAt).Round(time.Second)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm ago", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	}
}

func newWebStartCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Start the Web Companion if it is not running",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			before, _ := companion.Probe(cmd.Context(), home)
			status, spawned, err := companion.Ensure(cmd.Context(), home, companion.LifecycleKeep, 0, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !spawned {
				if before.Running && before.Mode == companion.LifecycleSession && status.Mode == companion.LifecycleKeep {
					fmt.Fprintf(out, "web companion promoted to keep mode\n  URL: %s\n", status.BaseURL)
				} else {
					fmt.Fprintf(out, "web companion already running\n  URL: %s\n", status.BaseURL)
				}
				return nil
			}
			fmt.Fprintf(out, "web companion started\n  URL:  %s\n", status.BaseURL)
			fmt.Fprintln(out, "It keeps running after this command exits. Stop it with: mm web stop")
			return nil
		},
	}
	return command
}

func newWebStopCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the Web Companion",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			if err := companion.Stop(cmd.Context(), home); err != nil {
				if strings.Contains(err.Error(), "not running") {
					fmt.Fprintln(cmd.OutOrStdout(), "web companion is not running")
					return nil
				}
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "web companion stopped")
			return nil
		},
	}
}

func newWebOpenCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "open [document]",
		Short: "Open the Web reader in the browser",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			box, err := runtime.get()
			if err != nil {
				return err
			}
			url := ""
			if len(args) == 1 {
				url, err = box.OpenDocumentWeb(cmd.Context(), args[0])
			} else {
				var status membox.WebStatusView
				status, err = box.EnsureWebCompanion(cmd.Context(), "")
				url = status.URL
			}
			if err != nil {
				return err
			}
			opener, err := runtime.launcher.OpenCommand(cmd.Context(), url)
			if err != nil {
				return err
			}
			if err := opener.Run(); err != nil {
				return fmt.Errorf("opening browser: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Opened %s\n", url)
			return nil
		},
	}
}

// newWebRunCommand is the hidden foreground companion runner. It is what the
// TUI spawns detached (`mm web run --lifecycle …`) and what
// `mm serve` reuses inline.
func newWebRunCommand(runtime *runtime) *cobra.Command {
	var port int
	var lifecycle string
	command := &cobra.Command{
		Use:    "run",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			return companion.Run(cmd.Context(), companion.Options{
				Home:      home,
				Port:      port,
				Lifecycle: lifecycle,
				Version:   membox.Version,
				OnReady: func(baseURL, token string) {
					fmt.Fprintf(cmd.OutOrStdout(), "web companion ready\n  URL:   %s\n  Token: %s\n", baseURL, token)
				},
			})
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	command.Flags().StringVar(&lifecycle, "lifecycle", companion.LifecycleSession, "lifecycle mode: session or keep")
	return command
}

// runServeForeground blocks as the Web Companion, printing pairing info once
// the server listens. Shared by `mm serve` (keep mode, user-facing).
func runServeForeground(ctx context.Context, runtime *runtime, port int, out func(format string, args ...any)) error {
	home, err := resolveHome(runtime)
	if err != nil {
		return err
	}
	return companion.Run(ctx, companion.Options{
		Home:      home,
		Port:      port,
		Lifecycle: companion.LifecycleKeep,
		Version:   membox.Version,
		OnReady: func(baseURL, token string) {
			out("membox serve\n")
			out("  URL:    %s\n", baseURL)
			out("  Token:  %s\n", token)
			out("\nPair the browser extension with this URL and token.\n")
			out("The TUI connects to this companion automatically while it runs.\n")
			out("Press Ctrl+C to stop.\n")
		},
	})
}
