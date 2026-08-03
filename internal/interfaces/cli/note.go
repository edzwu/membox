package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"membox"
)

func newNoteCommand(runtime *runtime) *cobra.Command {
	note := parentCommand("note", "Create and manage notes", "a note command is required")
	note.AddCommand(newNoteNewCommand(runtime), newNoteViewCommand(runtime))
	return note
}

func newNoteNewCommand(runtime *runtime) *cobra.Command {
	var fromSelector string
	var noOpen, jsonOutput bool
	command := &cobra.Command{Use: "new <title>", Short: "Create a note", Args: exactArgs(1, "note title")}
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
			fmt.Fprintf(cmd.OutOrStdout(), "Created note %s: %s\n", result.Document.ID, result.Document.Path)
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

func newNoteViewCommand(runtime *runtime) *cobra.Command {
	var webFlag, leafFlag, noOpen bool
	var port int
	command := &cobra.Command{Use: "view <document-id>", Short: "Open a note with the configured viewer (see 'mm config viewer')", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&webFlag, "web", false, "open in the browser this time (does not change the configured viewer)")
	command.Flags().BoolVar(&leafFlag, "leaf", false, "open with the leaf viewer this time (does not change the configured viewer)")
	command.Flags().BoolVar(&noOpen, "no-open", false, "with the web viewer, do not open the browser automatically")
	command.Flags().IntVar(&port, "port", 0, "with the web viewer, port to listen on (0 picks a free port)")
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
			server := box.WebServer()
			if _, err := server.Start(cmd.Context(), port); err != nil {
				return err
			}
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()

			viewURL := server.ViewURL(location.DocumentID)
			fmt.Fprintf(cmd.OutOrStdout(), "Serving %s\n", viewURL)

			if !noOpen {
				if opener, openErr := runtime.launcher.OpenCommand(cmd.Context(), viewURL); openErr == nil {
					_ = opener.Run()
				}
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Press Ctrl+C to stop.")
			<-cmd.Context().Done()
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
