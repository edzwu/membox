package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"membox"
)

func newNoteCommand(runtime *runtime) *cobra.Command {
	note := parentCommand("note", "Create and manage notes", "a note command is required")
	note.AddCommand(newNoteNewCommand(runtime))
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
