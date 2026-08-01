package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"membox"
	"membox/internal/interfaces/host"
)

func newDocCommand(runtime *runtime) *cobra.Command {
	doc := parentCommand("doc", "Find and operate on documents", "a doc command is required")
	doc.AddCommand(newDocListCommand(runtime), newDocSearchCommand(runtime), newDocShowCommand(runtime), newDocCatCommand(runtime), newDocEditCommand(runtime), newDocOpenCommand(runtime))
	return doc
}

func newDocListCommand(runtime *runtime) *cobra.Command {
	var limit int
	var all, jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List known Markdown documents", Args: noArgs}
	command.Flags().IntVar(&limit, "limit", 100, "maximum documents")
	command.Flags().BoolVar(&all, "all", false, "include missing and untracked documents")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		documents, err := box.ListDocuments(cmd.Context(), membox.ListDocumentsQuery{Limit: limit, All: all})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, documents)
		}
		if len(documents) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No documents.")
			return nil
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tTITLE\tSTATUS\tPATH")
		for _, document := range documents {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", shortID(document.ID), document.Title, document.Status, document.Path)
		}
		return writer.Flush()
	}
	return command
}

func newDocSearchCommand(runtime *runtime) *cobra.Command {
	var limit int
	var jsonOutput bool
	command := &cobra.Command{Use: "search <query>", Short: "Search indexed Markdown", Args: exactArgs(1, "query")}
	command.Flags().IntVar(&limit, "limit", 20, "maximum results")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		results, err := box.SearchDocuments(cmd.Context(), membox.SearchDocumentsQuery{Query: args[0], Limit: limit})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, results)
		}
		if len(results) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No results.")
			return nil
		}
		for _, result := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n%s\n", shortID(result.DocumentID), result.Title, result.Path)
			if result.Snippet != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result.Snippet)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}
	return command
}

func newDocShowCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "show <document-id>", Short: "Show document identity and state", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		document, err := box.GetDocument(cmd.Context(), membox.GetDocumentQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, document)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ID:          %s\n", document.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Path:        %s\n", document.Path)
		fmt.Fprintf(cmd.OutOrStdout(), "Status:      %s\n", document.Status)
		fmt.Fprintf(cmd.OutOrStdout(), "Title:       %s\n", document.Title)
		fmt.Fprintf(cmd.OutOrStdout(), "Size:        %d\n", document.Size)
		fmt.Fprintf(cmd.OutOrStdout(), "SHA-256:     %s\n", document.SHA256)
		if document.IndexedAt != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Indexed at:  %s\n", document.IndexedAt.Format(time.RFC3339))
		}
		return nil
	}
	return command
}

func newDocCatCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "cat <document-id>", Short: "Print the real Markdown file", Args: exactArgs(1, "document ID")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		body, err := box.ReadDocument(cmd.Context(), membox.ReadDocumentQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(body)
		return err
	}
	return command
}

func newDocEditCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "edit <document-id>", Short: "Edit the real Markdown file", Args: exactArgs(1, "document ID")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		location, err := box.ResolveDocumentLocation(cmd.Context(), membox.ResolveLocationQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if location.Status != "active" {
			return fmt.Errorf("document %s is %s at %s", location.DocumentID, location.Status, location.Path)
		}
		editor, err := runtime.launcher.EditorCommand(cmd.Context(), location.Path)
		if err != nil {
			return err
		}
		editor.Stdin, editor.Stdout, editor.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := editor.Run(); err != nil {
			return fmt.Errorf("editor failed: %w", err)
		}
		return box.ReindexDocument(cmd.Context(), membox.ReindexDocumentCommand{Selector: location.DocumentID})
	}
	return command
}

func newDocOpenCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "open <document-id>", Short: "Open the real Markdown file", Args: exactArgs(1, "document ID")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		location, err := box.ResolveDocumentLocation(cmd.Context(), membox.ResolveLocationQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if location.Status != "active" {
			return fmt.Errorf("document %s is %s at %s", location.DocumentID, location.Status, location.Path)
		}
		opener, err := runtime.launcher.OpenCommand(cmd.Context(), location.Path)
		if err != nil {
			return err
		}
		if err := opener.Run(); err != nil {
			return fmt.Errorf("opening document: %w", err)
		}
		return nil
	}
	return command
}

func shortID(id string) string { return host.ShortDocumentID(id) }
