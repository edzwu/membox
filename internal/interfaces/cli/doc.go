package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"membox"
	"membox/internal/interfaces/host"
	"membox/internal/web/companion"
)

func newDocCommand(runtime *runtime) *cobra.Command {
	doc := parentCommand("doc", "Find and operate on documents", "a doc command is required")
	doc.AddCommand(
		newDocNewCommand(runtime), newDocViewCommand(runtime), newDocListCommand(runtime),
		newDocSearchCommand(runtime), newDocShowCommand(runtime), newDocCatCommand(runtime),
		newDocEditCommand(runtime), newDocOpenCommand(runtime), newDocRenameCommand(runtime),
		newDocDeleteCommand(runtime), newDocMarkCommand(runtime), newDocSummarizeCommand(runtime),
		newDocRewriteCommand(runtime),
	)
	return doc
}

func newDocNewCommand(runtime *runtime) *cobra.Command {
	var fromSelector string
	var noOpen, jsonOutput bool
	command := &cobra.Command{Use: "new <title>", Short: "Create a document", Args: exactArgs(1, "document title")}
	command.Flags().StringVar(&fromSelector, "from", "", "source document selector to link from")
	command.Flags().BoolVar(&noOpen, "no-open", false, "create without opening the editor")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.CreateNote(cmd.Context(), membox.CreateNoteCommand{Title: args[0], FromSelector: fromSelector})
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeJSON(cmd, result); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Created document %s: %s\n", result.Document.ID, result.Document.Path)
			if result.Link != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Linked from: %s\n", result.Link.FromDocumentID)
			}
		}
		if noOpen {
			return nil
		}
		editor, err := runtime.launcher.EditorCommand(cmd.Context(), result.Document.Path)
		if err != nil {
			return err
		}
		editor.Stdin, editor.Stdout, editor.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := editor.Run(); err != nil {
			return fmt.Errorf("editor failed: %w", err)
		}
		return box.ReindexDocument(cmd.Context(), membox.ReindexDocumentCommand{Selector: result.Document.ID})
	}
	return command
}

func newDocViewCommand(runtime *runtime) *cobra.Command {
	var webFlag, leafFlag, noOpen bool
	command := &cobra.Command{Use: "view <document-id>", Short: "Open a document with the configured viewer (see 'mm config viewer')", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&webFlag, "web", false, "open in the browser this time (does not change the configured viewer)")
	command.Flags().BoolVar(&leafFlag, "leaf", false, "open with the leaf viewer this time (does not change the configured viewer)")
	command.Flags().BoolVar(&noOpen, "no-open", false, "with the web viewer, do not open the browser automatically")
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
		if strings.EqualFold(filepath.Ext(location.Path), ".pdf") {
			opener, openErr := runtime.launcher.OpenCommand(cmd.Context(), location.Path)
			if openErr != nil {
				return openErr
			}
			if openErr := opener.Run(); openErr != nil {
				return fmt.Errorf("opening PDF: %w", openErr)
			}
			return nil
		}
		viewer, err := box.GetViewer(cmd.Context())
		if err != nil {
			return err
		}
		if webFlag {
			viewer = "web"
		} else if leafFlag {
			viewer = "leaf"
		}
		if viewer == "web" {
			// The Web Companion owns the port; never spin up a second server.
			// Ensure with keep mode first: a CLI one-shot must not spawn a
			// session companion that dies the moment the command returns,
			// leaving the browser reader stranded. A running companion (e.g.
			// the TUI's) is reused as-is.
			if _, err := box.EnsureWebCompanion(cmd.Context(), companion.LifecycleKeep); err != nil {
				return err
			}
			viewURL, err := box.OpenDocumentWeb(cmd.Context(), location.DocumentID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Serving %s\n", viewURL)
			if !noOpen {
				if opener, openErr := runtime.launcher.OpenCommand(cmd.Context(), viewURL); openErr == nil {
					_ = opener.Run()
				}
			}
			return nil
		}

		viewerCmd, err := runtime.launcher.ViewerCommand(cmd.Context(), location.Path)
		if err != nil {
			return err
		}
		viewerCmd.Stdin, viewerCmd.Stdout, viewerCmd.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := viewerCmd.Run(); err != nil {
			return fmt.Errorf("viewer failed: %w", err)
		}
		return box.ReindexDocument(cmd.Context(), membox.ReindexDocumentCommand{Selector: location.DocumentID})
	}
	return command
}

func newDocListCommand(runtime *runtime) *cobra.Command {
	var limit int
	var all, jsonOutput bool
	var statusFilter string
	command := &cobra.Command{Use: "list", Short: "List known documents", Args: noArgs}
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
	command := &cobra.Command{Use: "search <query>", Short: "Search indexed documents", Args: exactArgs(1, "query")}
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
		// Human output shows the logical id. Physical UUIDs are a storage-layer
		// identity and never cross the Box boundary; DocumentView.ID is logical.
		logical := document.ID
		if logical == "" {
			logical = shortID(document.ID)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ID:          %s\n", logical)
		fmt.Fprintf(cmd.OutOrStdout(), "Path:        %s\n", document.Path)
		fmt.Fprintf(cmd.OutOrStdout(), "Status:      %s\n", document.Status)
		fmt.Fprintf(cmd.OutOrStdout(), "Title:       %s\n", document.Title)
		fmt.Fprintf(cmd.OutOrStdout(), "Media type:  %s\n", document.MediaType)
		if document.Authors != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Authors:     %s\n", document.Authors)
		}
		if document.Year != 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Year:        %d\n", document.Year)
		}
		if document.Keywords != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Keywords:    %s\n", document.Keywords)
		}
		if document.PageCount != 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Pages:       %d\n", document.PageCount)
		}
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
		document, err := box.GetDocument(cmd.Context(), membox.GetDocumentQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if document.MediaType != "text/markdown" {
			return fmt.Errorf("cat does not support %s; use 'mm doc open %s'", document.MediaType, args[0])
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
		if strings.EqualFold(filepath.Ext(location.Path), ".pdf") {
			return fmt.Errorf("PDFs are not text-editable; use 'mm pdf update %s' for metadata or 'mm pdf open %s'", args[0], args[0])
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
		Long: `Move the document file into the path's trash directory.
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
		Short: "Rename a document file without changing its UUID",
		Long: `Move the document file to a new filename in the same directory.
The stable UUID and relations are preserved. The extension must remain the
same supported media type. Use 'mm pdf rename' to also update PDF metadata.`,
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
