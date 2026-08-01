package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newIndexCommand(runtime *runtime) *cobra.Command {
	index := parentCommand("index", "Inspect the document index", "an index command is required")
	var jsonOutput bool
	status := &cobra.Command{Use: "status", Short: "Show catalog and index status", Args: noArgs}
	status.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	status.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.GetIndexStatus(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Paths:      %d\n", result.Paths)
		fmt.Fprintf(cmd.OutOrStdout(), "Active:     %d\n", result.Active)
		fmt.Fprintf(cmd.OutOrStdout(), "Missing:    %d\n", result.Missing)
		fmt.Fprintf(cmd.OutOrStdout(), "Untracked:  %d\n", result.Untracked)
		fmt.Fprintf(cmd.OutOrStdout(), "Database:   %s\n", result.DatabasePath)
		if result.LastScanAt != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Last scan:  %s\n", result.LastScanAt.Format("2006-01-02T15:04:05Z07:00"))
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Last scan:  never")
		}
		return nil
	}
	index.AddCommand(status)
	return index
}
