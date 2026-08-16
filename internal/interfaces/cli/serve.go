package cli

import (
	"github.com/spf13/cobra"
)

// newServeCommand keeps `mm serve` as a thin deprecated alias of
// `mm web start --fg`. The Web Companion control plane is `mm web`.
func newServeCommand(runtime *runtime) *cobra.Command {
	var port int
	var noToken bool

	command := &cobra.Command{
		Use:        "serve",
		Short:      "Alias for `mm web start --fg`",
		Deprecated: "use \"mm web start --fg\" (or \"mm web start\" for detached)",
		Hidden:     true, // single control plane is `mm web`; keep argv compat only
		Args:       noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWebForeground(cmd, runtime, port, noToken)
		},
	}
	command.Flags().IntVar(&port, "port", 0, "listen port (0 = prefer 8787, else ephemeral)")
	command.Flags().BoolVar(&noToken, "no-token", false, "disable bridge token auth (local dev only)")
	return command
}
