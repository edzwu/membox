package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newConfigCommand(runtime *runtime) *cobra.Command {
	config := parentCommand("config", "View and set membox configuration", "a config command is required")
	config.AddCommand(newConfigListCommand(runtime), newConfigSetCommand(runtime))
	return config
}

func newConfigListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List configuration settings", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		settings, err := box.ListSettings(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, settings)
		}
		out := cmd.OutOrStdout()
		for _, setting := range settings {
			value := setting.Value
			if value == "" {
				value = "(unset)"
			}
			fmt.Fprintf(out, "%-16s %s\n", setting.Key, value)
		}
		return nil
	}
	return command
}

func newConfigSetCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "set <key> <value>", Short: "Set a configuration value", Args: exactArgs(2, "key and value")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		if err := box.SetSetting(cmd.Context(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], args[1])
		return nil
	}
	return command
}
