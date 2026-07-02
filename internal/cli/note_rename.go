package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func noteRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <short-uuid> <new-title>",
		Short: "Rename a note by UUID prefix",
		Long: `Rename a note by its UUID prefix.

The markdown file is moved to the new title path and the metadata index is updated.
Duplicate titles are rejected.

Examples:
  mm note rename 5346a "Updated Title"
  mm note mv 5346a "Updated Title"`,
		Aliases: []string{"mv"},
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			uuidPrefix := args[0]
			newTitle := strings.Trim(args[1], `"`)

			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			n, err := deps.service.RenameNote(cmd.Context(), uuidPrefix, newTitle)
			if err != nil {
				return err
			}

			fmt.Printf("Renamed note %s: %q\n", n.UUID, n.Title)
			return nil
		},
	}
}
