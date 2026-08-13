package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"membox/internal/daemon"
)

func newVideoCommand(runtime *runtime) *cobra.Command {
	video := parentCommand("video", "Process video knowledge through mmd", "a video command is required")
	video.AddCommand(newVideoSummarizeCommand(runtime))
	return video
}

func newVideoSummarizeCommand(runtime *runtime) *cobra.Command {
	var courseCode string
	var lectureNo int
	var force, jsonOutput bool
	command := &cobra.Command{
		Use:   "summarize <youtube-url>",
		Short: "Download, summarize, and publish one lecture into membox through mmd",
		Args:  exactArgs(1, "YouTube URL"),
	}
	command.Flags().StringVar(&courseCode, "course", "", "stable readable course code, e.g. cs336 (required)")
	command.Flags().IntVar(&lectureNo, "lecture", 0, "lecture number (default: infer from readable video title, then playlist position)")
	command.Flags().BoolVar(&force, "force", false, "regenerate existing echo-bp artifacts")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	_ = command.MarkFlagRequired("course")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := daemon.DefaultConfig(runtime.home)
		if err != nil {
			return err
		}
		result, summarizeErr := daemon.VideoSummary(cmd.Context(), config, daemon.VideoSummaryRequest{
			URL: args[0], CourseCode: courseCode, LectureNo: lectureNo, Force: force,
		})
		if errors.Is(summarizeErr, daemon.ErrNotRunning) {
			return fmt.Errorf("mmd is not running for home %s", config.Home)
		}
		if summarizeErr != nil {
			return summarizeErr
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		action := "Updated"
		if result.Created {
			action = "Created"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s → %s\n", action, result.Filename, result.DocumentID)
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result.Path)
		return nil
	}
	return command
}
