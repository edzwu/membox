package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newClipCommand(runtime *runtime) *cobra.Command {
	clip := parentCommand("clip", "Browser clip helpers", "a clip command is required")
	clip.AddCommand(newClipProjectCommand(runtime))
	return clip
}

func newClipProjectCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "project",
		Short: "Rebuild Miru annotation projections for stored clips",
		Long: `Mirror every selection note (*-note.md) onto its page clip as a Miru
annotation (excerpt highlight + margin note). Runs automatically after each
scan (Ctrl+R); use this to migrate pre-existing notes explicitly. Idempotent.`,
		Args: noArgs,
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		pages, projected, err := box.ProjectClipAnnotations(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, map[string]int{"pages": pages, "projected": projected})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Projected %d selection note(s) across %d page clip(s).\n", projected, pages)
		return nil
	}
	return command
}
