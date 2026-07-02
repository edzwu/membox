package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func noteGetCmd() *cobra.Command {
	var flags struct {
		jsonOut bool
	}

	cmd := &cobra.Command{
		Use:     "get <uuid>",
		Aliases: []string{"show", "read"},
		Short:   "Display a single note",
		Long:    `Fetch a note by UUID and print its metadata and markdown content.`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			n, err := deps.service.GetNote(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if flags.jsonOut {
				printJSON(os.Stdout, n)
				return nil
			}

			fmt.Printf("UUID:      %s\n", n.UUID)
			fmt.Printf("Title:     %s\n", n.Title)
			fmt.Printf("Tags:      %v\n", n.Tags)
			fmt.Printf("Created:   %s\n", n.CreatedAt.Format(createDateFmt))
			fmt.Printf("Updated:   %s\n", n.UpdatedAt.Format(updateTimeFmt))
			fmt.Println("---")
			fmt.Println(n.Content)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.jsonOut, "json", false, "Output as JSON")
	return cmd
}
