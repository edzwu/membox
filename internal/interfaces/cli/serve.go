package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"membox"
)

func newServeCommand(runtime *runtime) *cobra.Command {
	var port int
	var noToken bool

	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the local Miru web server and browser-bridge endpoint",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			box, err := runtime.get()
			if err != nil {
				return err
			}

			// --no-token is a dev escape hatch: start without auth by using the
			// lower-level server path. Normal path always issues a token.
			if noToken {
				server := box.WebServer()
				baseURL, startErr := server.Start(cmd.Context(), port)
				if startErr != nil {
					return startErr
				}
				// Keep process-owned server alive the same way Box does.
				// Note: this bypasses Box.webServer; shutdown locally.
				defer func() {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					_ = server.Shutdown(shutdownCtx)
				}()
				fmt.Fprintf(cmd.OutOrStdout(), "membox serve\n  URL: %s\n  Auth disabled (--no-token)\nPress Ctrl+C to stop.\n", baseURL)
				<-cmd.Context().Done()
				return nil
			}

			baseURL, err := box.StartWebServer(cmd.Context(), port)
			if err != nil {
				return err
			}
			_, token := box.BridgeInfo()
			home := runtime.home
			if home == "" {
				config, cfgErr := membox.DefaultConfig()
				if cfgErr != nil {
					return cfgErr
				}
				home = config.Home
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "membox serve\n")
			fmt.Fprintf(out, "  URL:    %s\n", baseURL)
			fmt.Fprintf(out, "  Bridge: %s\n", filepath.Join(home, "bridge.json"))
			fmt.Fprintf(out, "  Token:  %s\n", token)
			fmt.Fprintf(out, "\nPair the browser extension with this URL and token.\n")
			fmt.Fprintf(out, "While the TUI is open it also starts this bridge automatically.\n")
			fmt.Fprintf(out, "Press Ctrl+C to stop.\n")

			<-cmd.Context().Done()
			// Box.Close (via runtime.close) shuts down the shared server.
			return nil
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	command.Flags().BoolVar(&noToken, "no-token", false, "disable bridge token auth (local dev only)")
	return command
}
