package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"membox"
)

func newLinkCommand(runtime *runtime) *cobra.Command {
	link := parentCommand("link", "Manage document links", "a link command is required")
	link.AddCommand(newLinkAddCommand(runtime), newLinkRemoveCommand(runtime), newLinkListCommand(runtime))
	return link
}

func newLinkAddCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "add <from-id> <to-id>", Short: "Link two documents", Args: exactArgs(2, "document selectors")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.LinkDocuments(cmd.Context(), membox.LinkDocumentsCommand{FromSelector: args[0], ToSelector: args[1]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.AlreadyExists {
			fmt.Fprintln(cmd.OutOrStdout(), "Document link already exists.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Created document link.")
		}
		return nil
	}
	return command
}

func newLinkRemoveCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "remove <from-id> <to-id>", Short: "Remove a document link", Args: exactArgs(2, "document selectors")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.UnlinkDocuments(cmd.Context(), membox.UnlinkDocumentsCommand{FromSelector: args[0], ToSelector: args[1]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.Removed {
			fmt.Fprintln(cmd.OutOrStdout(), "Removed document link.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Document link did not exist.")
		}
		return nil
	}
	return command
}

func newLinkListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list <document-id>", Short: "Show document links and topics", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		graph, err := box.GetDocumentGraph(cmd.Context(), membox.GetDocumentGraphQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, graph)
		}
		printDocumentLinks(cmd, graph)
		return nil
	}
	return command
}

func printDocumentLinks(cmd *cobra.Command, graph membox.DocumentGraphView) {
	fmt.Fprintf(cmd.OutOrStdout(), "Outgoing links: %d\n", len(graph.Outgoing))
	for _, document := range graph.Outgoing {
		fmt.Fprintf(cmd.OutOrStdout(), "  → %s  %s\n", shortID(document.ID), displayName(document.Title, document.Path))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Incoming links: %d\n", len(graph.Incoming))
	for _, document := range graph.Incoming {
		fmt.Fprintf(cmd.OutOrStdout(), "  ← %s  %s\n", shortID(document.ID), displayName(document.Title, document.Path))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Topics: %d\n", len(graph.Topics))
	for _, topic := range graph.Topics {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s\n", shortID(topic.ID), topic.Name)
	}
}
