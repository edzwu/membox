package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"membox/internal/web/companion"
)

func newServeCommand(runtime *runtime) *cobra.Command {
	var port int
	var noToken bool

	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the local Miru web server and browser-bridge endpoint",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(runtime)
			if err != nil {
				return err
			}
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
				fmt.Fprintf(cmd.OutOrStdout(), "membox serve\n  URL: %s\n  Auth disabled (--no-token)\nPress Ctrl+C to stop.\n", baseURL)
				<-cmd.Context().Done()
				return nil
			}

			if status, _ := companion.Probe(cmd.Context(), home); status.Running {
				return fmt.Errorf("a web companion is already running: %s (stop it with: mm web stop)", status.BaseURL)
			}
			out := cmd.OutOrStdout()
			err = runServeForeground(cmd.Context(), runtime, port, func(format string, args ...any) {
				fmt.Fprintf(out, format, args...)
			})
			if err == context.Canceled {
				return nil
			}
			return err
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	command.Flags().BoolVar(&noToken, "no-token", false, "disable bridge token auth (local dev only)")
	return command
}
