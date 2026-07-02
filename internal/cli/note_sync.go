package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func noteSyncCmd() *cobra.Command {
	var flags struct {
		verbose bool
	}

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync metadata index from markdown files",
		Long: `Scan the filesystem notes directory and rebuild the SQLite metadata index.

This is useful when markdown files are edited outside of mm or when the
metadata index is out of date.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			res, err := deps.service.Sync(cmd.Context())
			if err != nil {
				return err
			}

			fmt.Printf("Synced %d notes: %d created, %d updated, %d unchanged, %d deleted, %d errors\n",
				res.Total, res.Created, res.Updated, res.Unchanged, res.Deleted, res.Errors)
			if flags.verbose {
				for _, e := range res.Errs {
					fmt.Fprintf(os.Stderr, "  error: %v\n", e)
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(
		&flags.verbose, "verbose", false, "Print per-note errors")
	return cmd
}
