package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func noteSearchCmd() *cobra.Command {
	var flags struct {
		limit   int
		jsonOut bool
	}

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search notes",
		Long:  `Search note titles using SQLite FTS5.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			results, err := deps.service.SearchNotes(cmd.Context(), args[0], flags.limit)
			if err != nil {
				return err
			}

			if flags.jsonOut {
				printJSON(os.Stdout, results)
				return nil
			}

			if len(results) == 0 {
				fmt.Println("No results.")
				return nil
			}

			for _, r := range results {
				n := r.Note
				fmt.Printf("%s  %s\n", n.UUID, n.Title)
				if r.Snippet != "" {
					fmt.Printf("    %s\n", r.Snippet)
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.IntVar(
		&flags.limit, "limit", 20, "Maximum number of results")
	f.BoolVar(
		&flags.jsonOut, "json", false, "Output as JSON")

	return cmd
}
