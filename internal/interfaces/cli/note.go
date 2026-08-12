package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newNoteCommand keeps `mm note` working as a deprecated alias for `mm doc`:
// a note is just a document, so the commands live under doc now. The
// deprecation warning goes to stderr explicitly (not cobra's Deprecated
// field, which pollutes stdout and breaks --json consumers).
func newNoteCommand(runtime *runtime) *cobra.Command {
	note := parentCommand("note", "Alias for 'mm doc' (deprecated)", "a note command is required")
	note.Hidden = true
	note.AddCommand(
		deprecatedAlias(newDocNewCommand(runtime), "doc new"),
		deprecatedAlias(newDocViewCommand(runtime), "doc view"),
	)
	return note
}

func deprecatedAlias(command *cobra.Command, replacement string) *cobra.Command {
	runE := command.RunE
	command.RunE = func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(), "note %s is deprecated: a note is just a document, use 'mm %s' instead\n", command.Name(), replacement)
		return runE(cmd, args)
	}
	return command
}
