package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"membox"
)

func newPathCommand(runtime *runtime) *cobra.Command {
	path := parentCommand("path", "Manage document scan paths", "a path command is required")
	path.AddCommand(newPathAddCommand(runtime), newPathListCommand(runtime), newPathRemoveCommand(runtime), newPathScanCommand(runtime))
	return path
}

func newPathAddCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "add <directory>", Short: "Add a Markdown/PDF scan path", Args: exactArgs(1, "directory")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, addErr := box.AddPath(cmd.Context(), membox.AddPathCommand{Directory: args[0]})
		if jsonOutput {
			if err := writeJSON(cmd, result); err != nil {
				return err
			}
		} else if result.Path.ID != 0 {
			if result.AlreadyExists {
				fmt.Fprintf(cmd.OutOrStdout(), "Path %d is already configured: %s\n", result.Path.ID, result.Path.Path)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Added path %d: %s\n", result.Path.ID, result.Path.Path)
				printScanSummary(cmd, result.Scan)
			}
		}
		return addErr
	}
	return command
}

func newPathListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List configured paths", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		paths, err := box.ListPaths(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, paths)
		}
		if len(paths) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No configured paths.")
			return nil
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tPATH\tDOCS\tSTATUS\tLAST SCAN")
		for _, path := range paths {
			fmt.Fprintf(writer, "%d\t%s\t%d\t%s\t%s\n", path.ID, path.Path, path.Documents, path.Status, relativeTime(path.LastScanAt))
		}
		return writer.Flush()
	}
	return command
}

func newPathRemoveCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "remove <path-id-or-directory>", Short: "Remove a configured path", Args: exactArgs(1, "path selector")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.RemovePath(cmd.Context(), membox.RemovePathCommand{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed path %d; %d document(s) are now untracked.\n", result.PathID, result.Documents)
		return nil
	}
	return command
}

func newPathScanCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	var timestampSource string
	command := &cobra.Command{Use: "scan [path-id-or-directory]", Short: "Scan configured paths", Args: maxArgs(1)}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.Flags().StringVar(&timestampSource, "timestamp", "filesystem", "document timestamp source: filesystem or git")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		selector := ""
		if len(args) == 1 {
			selector = args[0]
		}
		box, err := runtime.get()
		if err != nil {
			return err
		}
		report, scanErr := box.ScanPaths(cmd.Context(), membox.ScanPathsCommand{Selector: selector, TimestampSource: timestampSource})
		if jsonOutput {
			if err := writeJSON(cmd, report); err != nil {
				return err
			}
		} else {
			printScanSummary(cmd, report)
		}
		return scanErr
	}
	return command
}

func printScanSummary(cmd *cobra.Command, report membox.ScanReport) {
	fmt.Fprintf(cmd.OutOrStdout(), "Scanned %d path(s) and %d document file(s)\n", report.Paths, report.Files)
	fmt.Fprintf(cmd.OutOrStdout(), "  added:             %d\n", report.Added)
	fmt.Fprintf(cmd.OutOrStdout(), "  updated:           %d\n", report.Updated)
	fmt.Fprintf(cmd.OutOrStdout(), "  renamed:           %d\n", report.Renamed)
	fmt.Fprintf(cmd.OutOrStdout(), "  unchanged:         %d\n", report.Unchanged)
	fmt.Fprintf(cmd.OutOrStdout(), "  missing:           %d\n", report.Missing)
	fmt.Fprintf(cmd.OutOrStdout(), "  possible renames:  %d\n", report.PossibleRenames)
	fmt.Fprintf(cmd.OutOrStdout(), "  errors:            %d\n", report.Errors)
	if report.TimestampSource == "git" {
		fmt.Fprintln(cmd.OutOrStdout(), "Git timestamps")
		fmt.Fprintf(cmd.OutOrStdout(), "  Git paths:         %d\n", report.GitPaths)
		fmt.Fprintf(cmd.OutOrStdout(), "  updated:           %d\n", report.TimestampsUpdated)
		fmt.Fprintf(cmd.OutOrStdout(), "  unchanged:         %d\n", report.TimestampsUnchanged)
		fmt.Fprintf(cmd.OutOrStdout(), "  no Git history:    %d\n", report.NoGitHistory)
		fmt.Fprintf(cmd.OutOrStdout(), "  non-Git paths:     %d\n", report.NonGitPaths)
	}
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func relativeTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	duration := time.Since(*value)
	if duration < time.Minute {
		return "just now"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}
