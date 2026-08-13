package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"membox/internal/daemon"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRoot().ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	var home string
	root := &cobra.Command{
		Use:           "mmd",
		Short:         "Membox local backend daemon",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&home, "home", "", "membox home directory (default: MEMBOX_HOME or ~/.membox)")
	root.AddCommand(newRunCommand(&home), newStatusCommand(&home), newStopCommand(&home))
	return root
}

func config(home *string) (daemon.Config, error) {
	return daemon.DefaultConfig(*home)
}

func newRunCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the mmd daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config(home)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "mmd home: %s\n", cfg.Home)
			fmt.Fprintf(cmd.OutOrStdout(), "mmd socket: %s\n", cfg.SocketPath)
			fmt.Fprintln(cmd.OutOrStdout(), "mmd is running; press Ctrl+C or use 'mmd stop' to stop it")
			return daemon.New(cfg).Run(cmd.Context())
		},
	}
}

func newStatusCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the mmd daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config(home)
			if err != nil {
				return err
			}
			health, err := daemon.Healthcheck(cfg)
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Fprintf(cmd.OutOrStdout(), "mmd is not running\n  Socket: %s\n", cfg.SocketPath)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "mmd is running\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  PID:      %d\n", health.PID)
			fmt.Fprintf(cmd.OutOrStdout(), "  Database: %s\n", health.Database)
			fmt.Fprintf(cmd.OutOrStdout(), "  Objects:  %s\n", health.Objects)
			fmt.Fprintf(cmd.OutOrStdout(), "  Socket:   %s\n", health.Socket)
			fmt.Fprintf(cmd.OutOrStdout(), "  Started:  %s\n", health.Started.Local().Format(time.RFC3339))
			return nil
		},
	}
}

func newStopCommand(home *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Ask the mmd daemon to shut down gracefully",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config(home)
			if err != nil {
				return err
			}
			if err := daemon.Stop(cmd.Context(), cfg); errors.Is(err, daemon.ErrNotRunning) {
				fmt.Fprintln(cmd.OutOrStdout(), "mmd is not running")
				return nil
			} else if err != nil {
				return err
			}
			waitCtx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			if err := daemon.WaitStopped(waitCtx, cfg); err != nil {
				return fmt.Errorf("waiting for mmd to stop: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "mmd stopped")
			return nil
		},
	}
}
