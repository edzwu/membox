package cli

import (
	"fmt"
	"os"

	"github.com/earendil-works/membox/internal/tui"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the full mm command tree.
// The root command itself launches the interactive TUI; subcommands expose
// non-interactive operations such as init and note management.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mm",
		Short: "membox: a distributed knowledge-base CLI",
		Long: `membox is the local control plane for the membox knowledge base.

It manages notes (Markdown files with UUID metadata) and uses the XDG Base
Directory Specification by default. Set MM_DEV=1 (or run 'make run') to keep
config and data under ./.membox in the current working directory.

Run 'mm init' to create a local development workspace.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run()
		},
	}

	root.AddCommand(initCmd())
	root.AddCommand(noteCmd())
	root.AddCommand(workspaceCmd())
	root.AddCommand(perkeepCmd())

	return root
}

// Execute is the legacy entrypoint used by cmd/mm/main.go.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mm: %v\n", err)
		os.Exit(1)
	}
}
