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
	doc.AddCommand(newDocListCommand(runtime), newDocSearchCommand(runtime), newDocShowCommand(runtime), newDocCatCommand(runtime), newDocEditCommand(runtime), newDocOpenCommand(runtime), newDocRenameCommand(runtime), newDocDeleteCommand(runtime), newDocMarkCommand(runtime))
	return doc
}

func newDocListCommand(runtime *runtime) *cobra.Command {
	var limit int
	var all, jsonOutput bool
	var statusFilter string
	command := &cobra.Command{Use: "list", Short: "List known Markdown documents", Args: noArgs}
	command.Flags().IntVar(&limit, "limit", 100, "maximum documents")
	command.Flags().BoolVar(&all, "all", false, "include missing and untracked documents")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.Flags().StringVar(&statusFilter, "status", "", "filter by read status: unread | reading | finished")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		documents, err := box.ListDocuments(cmd.Context(), membox.ListDocumentsQuery{Limit: limit, All: all, Status: statusFilter})
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
		fmt.Fprintln(writer, "ID\tTITLE\tREAD\tSTATUS\tPATH")
		for _, document := range documents {
			read := document.ReadStatus
			if read == "" {
				read = "unread"
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", shortID(document.ID), document.Title, read, document.Status, document.Path)
		}
		return writer.Flush()
	}
	return command
}

func newDocMarkCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "mark <document-id> <status>", Short: "Set read status: unread | reading | finished", Args: exactArgs(2, "document ID and read status")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		if err := box.SetDocumentReadStatus(cmd.Context(), membox.SetReadStatusCommand{Selector: args[0], Status: args[1]}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s marked %s\n", args[0], args[1])
		return nil
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
		fmt.Fprintf(cmd.OutOrStdout(), "Created at:  %s\n", document.CreatedAt.Format(time.RFC3339))
		fmt.Fprintf(cmd.OutOrStdout(), "Modified at: %s\n", document.UpdatedAt.Format(time.RFC3339))
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

func displayName(title, path string) string {
	if title != "" {
		return title
	}
	return path
}

func shortID(id string) string { return host.ShortDocumentID(id) }

func newDocDeleteCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "delete <document-id>",
		Short: "Move a document to the trash (soft delete)",
		Long: `Move the document's Markdown file into the path's trash directory.
The document keeps its UUID, links, and annotations; restore it with
'mm trash restore <document-id>' or empty the trash with 'mm trash purge'.`,
		Args: exactArgs(1, "document ID"),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.DeleteDocument(cmd.Context(), membox.DeleteDocumentCommand{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Moved %s to trash: %s\n", result.DocumentID, result.Path)
		return nil
	}
	return command
}

func newDocRenameCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "rename <document-id> <new-filename>",
		Short: "Rename the Markdown file without changing the document UUID",
		Long: `Move the document's Markdown file to a new filename in the same
directory. The stable UUID, graph links, source URLs, and annotation
relations are preserved. The new name must stay a Markdown file.`,
		Args: exactArgs(2, "document ID and new filename"),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.RenameDocument(cmd.Context(), membox.RenameDocumentCommand{Selector: args[0], NewFilename: args[1]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Renamed document %s: %s\n", result.DocumentID, result.Path)
		return nil
	}
	return command
}
