package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"membox"
)

func newQuestionCommand(runtime *runtime) *cobra.Command {
	question := parentCommand("question", "Accumulate, manage, and export personal questions", "a question command is required")
	question.AddCommand(
		newQuestionAddCommand(runtime),
		newQuestionListCommand(runtime),
		newQuestionAnswerCommand(runtime),
		newQuestionArchiveCommand(runtime),
		newQuestionDeleteCommand(runtime),
		newQuestionExportCommand(runtime),
	)
	return question
}

func newQuestionAddCommand(runtime *runtime) *cobra.Command {
	var source string
	command := &cobra.Command{Use: "add <question>", Short: "Record a new question", Args: exactArgs(1, "question text")}
	command.Flags().StringVar(&source, "doc", "", "optional source document id")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.AddQuestion(cmd.Context(), membox.AddQuestionCommand{Body: args[0], SourceDocumentID: source})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added question %s (%s)\n", shortID(view.ID), view.Status)
		return nil
	}
	return command
}

func newQuestionListCommand(runtime *runtime) *cobra.Command {
	var status string
	var limit int
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List questions (open first, newest first)", Args: exactArgs(0, "")}
	command.Flags().StringVar(&status, "status", "", "filter: open|answered|archived (default all)")
	command.Flags().IntVar(&limit, "limit", 50, "max rows")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		questions, err := box.ListQuestions(cmd.Context(), membox.ListQuestionsCommand{Status: normalizeStatusFlag(status), Limit: limit})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, questions)
		}
		if len(questions) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No questions.")
			return nil
		}
		for _, q := range questions {
			mark := "[ ]"
			if q.Status == "answered" {
				mark = "[x]"
			} else if q.Status == "archived" {
				mark = "[·]"
			}
			line := fmt.Sprintf("%s %-10s %s  %s", mark, shortID(q.ID), q.CreatedAt.Format("2006-01-02"), oneLine(q.Body))
			if q.SourceDocumentID != "" {
				line += fmt.Sprintf("  (doc %s)", shortID(q.SourceDocumentID))
			}
			if q.Answer != "" {
				line += fmt.Sprintf("\n    ↳ %s", oneLine(q.Answer))
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		return nil
	}
	return command
}

func newQuestionAnswerCommand(runtime *runtime) *cobra.Command {
	var answer string
	command := &cobra.Command{Use: "answer <id>", Short: "Mark a question answered with optional answer text", Args: exactArgs(1, "question id")}
	command.Flags().StringVar(&answer, "answer", "", "answer text (marks it answered)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.AnswerQuestion(cmd.Context(), membox.AnswerQuestionCommand{Selector: args[0], Answer: answer})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s → %s\n", shortID(view.ID), view.Status)
		return nil
	}
	return command
}

func newQuestionArchiveCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "archive <id>", Short: "Archive a question without answering", Args: exactArgs(1, "question id")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.ArchiveQuestion(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s → %s\n", shortID(view.ID), view.Status)
		return nil
	}
	return command
}

func newQuestionDeleteCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "delete <id>", Short: "Delete a question permanently", Args: exactArgs(1, "question id")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		if err := box.DeleteQuestion(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", shortID(args[0]))
		return nil
	}
	return command
}

func newQuestionExportCommand(runtime *runtime) *cobra.Command {
	var status, out string
	command := &cobra.Command{Use: "export", Short: "Export questions to a Markdown file", Args: exactArgs(0, "")}
	command.Flags().StringVar(&status, "status", "", "export only: open|answered|archived (default all)")
	command.Flags().StringVar(&out, "out", "", "output file (default questions-export.md)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		path, err := box.ExportQuestions(cmd.Context(), membox.ExportQuestionsCommand{Status: normalizeStatusFlag(status), Out: out})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Exported questions to %s\n", path)
		return nil
	}
	return command
}

func normalizeStatusFlag(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "answered", "open", "archived":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "" // all
	}
}

func oneLine(text string) string {
	return strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
}
