package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newConfigCommand(runtime *runtime) *cobra.Command {
	config := parentCommand("config", "Show and change membox settings", "a config command is required")
	config.AddCommand(newConfigViewerCommand(runtime))
	return config
}

func newConfigViewerCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "viewer [leaf|web]",
		Short: "Show or set the default document viewer",
		Args:  maxArgs(1),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			if err := box.SetViewer(cmd.Context(), args[0]); err != nil {
				return err
			}
		}
		viewer, err := box.GetViewer(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, map[string]string{"viewer": viewer})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "viewer: %s\n", viewer)
		return nil
	}
	return command
}
