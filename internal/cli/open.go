package cli

import (
	"github.com/spf13/cobra"
)

func openCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <uuid>",
		Short: "Open a note in the configured editor",
		Long: `Open the markdown file for a note by UUID in the editor configured
by the EDITOR environment variable (default: vim).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			if err := deps.service.OpenNote(cmd.Context(), args[0]); err != nil {
				return err
			}
			return nil
		},
	}
}
