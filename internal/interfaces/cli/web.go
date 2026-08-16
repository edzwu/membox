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

// newWebCommand is the single control plane for the Web Companion — the one
// process per membox home that owns Miru, the browser bridge, and web-originated
// writes. Foreground and detached are just start modes; TUI/CLI/extension all
// share the same companion.
func newWebCommand(runtime *runtime) *cobra.Command {
	command := parentCommand("web", "Control the membox Web Companion", "web command is required: status, start, stop, restart, open")
	command.AddCommand(
		newWebStatusCommand(runtime),
		newWebStartCommand(runtime),
		newWebStopCommand(runtime),
		newWebRestartCommand(runtime),
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
			if status.Token != "" {
				fmt.Fprintf(out, "  Token:    %s\n", status.Token)
				fmt.Fprintln(out, "Paste URL + Token into the browser extension popup, then Save settings.")
			}
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
	var port int
	var foreground bool
	var noToken bool
	command := &cobra.Command{
		Use:   "start",
		Short: "Start the Web Companion (detached by default; --fg runs in this terminal)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if noToken {
				foreground = true
			}
			if foreground {
				return runWebForeground(cmd, runtime, port, noToken)
			}
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			status, spawned, err := companion.Ensure(cmd.Context(), home, companion.LifecycleKeep, port, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !spawned {
				fmt.Fprintf(out, "web companion already running\n  URL: %s\n", status.BaseURL)
				return nil
			}
			fmt.Fprintf(out, "web companion started\n  URL:  %s\n", status.BaseURL)
			fmt.Fprintln(out, "It keeps running after this command exits. Stop it with: mm web stop")
			return nil
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	command.Flags().BoolVar(&foreground, "fg", false, "run in the foreground until Ctrl+C (pair clipper / debug)")
	command.Flags().BoolVar(&noToken, "no-token", false, "disable bridge token auth (local dev only; implies --fg)")
	return command
}

// runWebForeground blocks as the Web Companion in this process. Shared by
// `mm web start --fg` and the deprecated `mm serve` alias.
func runWebForeground(cmd *cobra.Command, runtime *runtime, port int, noToken bool) error {
	home, err := resolveHome(runtime)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	// --no-token skips HTTP auth only. It cannot share a home with a
	// companion; all data mutations still use the home mutation lock.
	if noToken {
		releaseOwnership, lockErr := companion.AcquireLock(home)
		if lockErr != nil {
			return fmt.Errorf("claiming web ownership for --no-token: %w", lockErr)
		}
		defer releaseOwnership()
		box, err := runtime.get()
		if err != nil {
			return err
		}
		server := box.WebServer()
		baseURL, startErr := server.Start(cmd.Context(), port)
		if startErr != nil {
			return startErr
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		fmt.Fprintf(out, "web companion (foreground)\n  URL: %s\n  Auth disabled (--no-token)\nPress Ctrl+C to stop.\n", baseURL)
		<-cmd.Context().Done()
		return nil
	}

	if status, _ := companion.Probe(cmd.Context(), home); status.Running {
		return fmt.Errorf("a web companion is already running: %s (stop it with: mm web stop)", status.BaseURL)
	}
	err = runServeForeground(cmd.Context(), runtime, port, func(format string, args ...any) {
		fmt.Fprintf(out, format, args...)
	})
	if err == context.Canceled {
		return nil
	}
	return err
}

func newWebRestartCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the Web Companion (stop orphans, bind preferred port)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
			// Keep mode so the companion survives this CLI command exiting;
			// the TUI applies its own keep/stop policy on quit.
			status, err := companion.Restart(cmd.Context(), home, companion.LifecycleKeep, 0, nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "web companion restarted\n  URL:  %s\n  PID:  %d\n", status.BaseURL, status.PID)
			return nil
		},
	}
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
// TUI / `mm web start` spawn detached (`mm web run …`).
func newWebRunCommand(runtime *runtime) *cobra.Command {
	var port int
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
				Lifecycle: companion.LifecycleKeep,
				Version:   membox.Version,
				OnReady: func(baseURL, token string) {
					fmt.Fprintf(cmd.OutOrStdout(), "web companion ready\n  URL:   %s\n  Token: %s\n", baseURL, token)
				},
			})
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	return command
}

// runServeForeground blocks as the Web Companion, printing pairing info once
// the server listens. Used by `mm web start --fg` (and the `mm serve` alias).
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
			out("web companion (foreground)\n")
			out("  URL:    %s\n", baseURL)
			out("  Token:  %s\n", token)
			out("\nPair the browser extension with this URL and token.\n")
			out("Detached alternative: mm web start\n")
			out("Press Ctrl+C to stop.\n")
		},
	})
}
