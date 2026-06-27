package cli

import (
	"github.com/earendil-works/membox/internal/tui"
	"github.com/spf13/cobra"
)

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Start the interactive membox TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run()
		},
	}
}
