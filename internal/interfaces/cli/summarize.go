package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"membox"
)

func newDocSummarizeCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "summarize <document-id> [summary]",
		Short: "Set a document summary; omit the text to generate one with the agent",
		Long: `Set the summary shown on document cards (TUI/web) and kept in the index.

With a summary argument the text is stored as-is:

    mm doc summarize <id> "要点：……"

Without it, the agent (via the Web Companion) reads the document and writes
a summary itself:

    mm doc summarize <id>

Summaries survive rescans: reindexing never overwrites them.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageErr(cmd, "missing document ID")
			}
			if len(args) > 2 {
				return usageErr(cmd, fmt.Sprintf("unexpected argument %q", args[2]))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		selector := args[0]

		if len(args) == 2 {
			summary := strings.TrimSpace(args[1])
			if summary == "" {
				return fmt.Errorf("summary text is empty")
			}
			view, err := box.SetDocumentSummary(ctx, membox.SetSummaryCommand{Selector: selector, Summary: summary})
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd, view)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Summary saved for %s (%s):\n\n%s\n", shortID(view.ID), view.Title, view.Summary)
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Generating summary with the agent…")
		view, err := box.SummarizeDocument(ctx, selector)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, view)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Summary saved for %s (%s):\n\n%s\n", shortID(view.ID), view.Title, view.Summary)
		return nil
	}
	return command
}
