package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func noteDeleteCmd() *cobra.Command {
	var flags struct {
		force bool
	}

	cmd := &cobra.Command{
		Use:     "delete <uuid>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a note",
		Long:    `Delete a note by UUID from both the filesystem and the metadata index.`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flags.force {
				return fmt.Errorf("refusing to delete without --force")
			}

			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			if err := deps.service.DeleteNote(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted note %s\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.force, "force", false, "Confirm deletion")
	return cmd
}
