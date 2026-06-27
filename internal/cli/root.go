package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd builds the full mm command tree.
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
	}

	root.AddCommand(initCmd())
	root.AddCommand(tuiCmd())

	return root
}

// Execute is the legacy entrypoint used by cmd/mm/main.go.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mm: %v\n", err)
		os.Exit(1)
	}
}
