package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mmd",
		Short: "membox daemon",
		Long: `mmd is the background daemon for membox.

It manages the local workspace, keeps an SSH tunnel open when configured,
and periodically syncs notes to a Perkeep server.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newStartCmd())
	root.AddCommand(newCheckCmd())
	return root
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the membox daemon",
		Long:  `Start the daemon in the foreground. It will load or prompt for settings, then manage tunnels and sync.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon()
		},
	}
}

func newCheckCmd() *cobra.Command {
	var upload bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Test Perkeep connectivity",
		Long:  `Print the resolved configuration and test whether the Perkeep server is reachable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(upload)
		},
	}
	cmd.Flags().BoolVar(&upload, "upload", false, "Upload a small test file via pk-put to verify the full write path")
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mmd: %v\n", err)
		os.Exit(1)
	}
}
