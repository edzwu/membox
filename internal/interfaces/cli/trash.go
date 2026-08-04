package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"membox"
)

func newTrashCommand(runtime *runtime) *cobra.Command {
	trash := parentCommand("trash", "Manage the trash (soft-deleted documents)", "a trash command is required")
	trash.AddCommand(newTrashListCommand(runtime), newTrashRestoreCommand(runtime), newTrashPurgeCommand(runtime))
	return trash
}

func newTrashListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List trashed documents", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		items, err := box.ListTrash(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, items)
		}
		out := cmd.OutOrStdout()
		var total int64
		for _, item := range items {
			total += item.Size
			fmt.Fprintf(out, "%s  %-19s  %s\n",
				shortID(item.ID), item.TrashedAt.Local().Format("2006-01-02 15:04:05"),
				displayName(item.Title, item.OriginRelativePath))
		}
		fmt.Fprintf(out, "Trash: %d document(s), %s. Restore: mm trash restore <document-id>; empty: mm trash purge\n",
			len(items), formatBytes(total))
		return nil
	}
	return command
}

func newTrashRestoreCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "restore <document-id>", Short: "Restore a trashed document to its original path", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.RestoreTrashedDocument(cmd.Context(), membox.RestoreDocumentCommand{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Restored %s to %s\n", shortID(result.DocumentID), result.Path)
		return nil
	}
	return command
}

func newTrashPurgeCommand(runtime *runtime) *cobra.Command {
	var jsonOutput, all bool
	var olderThanDays int
	command := &cobra.Command{Use: "purge", Short: "Permanently delete trashed documents", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.Flags().BoolVar(&all, "all", false, "purge everything in the trash")
	command.Flags().IntVar(&olderThanDays, "older", 30, "only purge items trashed more than N days ago")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.PurgeTrash(cmd.Context(), membox.PurgeTrashCommand{All: all, OlderThanDays: olderThanDays})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.Removed == 0 {
			scope := fmt.Sprintf("older than %d days", olderThanDays)
			if all {
				scope = "in the trash"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Nothing %s to purge.\n", scope)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Purged %d document(s), freed %s.\n", result.Removed, formatBytes(result.BytesFreed))
		return nil
	}
	return command
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
