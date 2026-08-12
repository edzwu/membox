package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"membox"
	"membox/internal/daemon"
)

// newScanCommand is the first mm command backed exclusively by mmd. The
// existing `mm path scan` remains available during migration for clients that
// still open the catalog in-process.
func newScanCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	var timestampSource string
	command := &cobra.Command{
		Use:   "scan [path-id-or-directory]",
		Short: "Scan through the mmd backend and capture new content versions",
		Args:  maxArgs(1),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.Flags().StringVar(&timestampSource, "timestamp", "filesystem", "document timestamp source: filesystem or git")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		selector := ""
		if len(args) == 1 {
			selector = args[0]
		}
		config, err := daemon.DefaultConfig(runtime.home)
		if err != nil {
			return err
		}
		report, scanErr := daemon.Scan(cmd.Context(), config, daemon.ScanRequest{
			Selector: selector, TimestampSource: timestampSource,
		})
		if errors.Is(scanErr, daemon.ErrNotRunning) {
			return fmt.Errorf("mmd is not running for home %s", config.Home)
		}
		view := scanReportView(report)
		if jsonOutput {
			if err := writeJSON(cmd, view); err != nil {
				return err
			}
		} else {
			printScanSummary(cmd, view)
		}
		return scanErr
	}
	return command
}

func scanReportView(report daemon.ScanReport) membox.ScanReport {
	return membox.ScanReport{
		Paths: report.Paths, Files: report.Files, Added: report.Added, Updated: report.Updated,
		Renamed: report.Renamed, Unchanged: report.Unchanged, Missing: report.Missing,
		PossibleRenames: report.PossibleRenames, Errors: report.Errors,
		TimestampSource: report.TimestampSource, GitPaths: report.GitPaths,
		TimestampsUpdated: report.TimestampsUpdated, TimestampsUnchanged: report.TimestampsUnchanged,
		NoGitHistory: report.NoGitHistory, NonGitPaths: report.NonGitPaths,
	}
}
