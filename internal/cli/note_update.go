package cli

import (
	"fmt"
	"os"

	"github.com/earendil-works/membox/internal/service"
	"github.com/spf13/cobra"
)

func noteUpdateCmd() *cobra.Command {
	var flags struct {
		title   string
		tags    []string
		content string
		jsonOut bool
	}

	cmd := &cobra.Command{
		Use:   "update <uuid>",
		Short: "Update an existing note",
		Long: `Update a note by UUID. Only supplied fields are changed.

Examples:
  mm note update <uuid> --title "New title"
  mm note update <uuid> --content "New content"
  mm note update <uuid> --tag go --tag sqlite`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			req := service.UpdateNoteRequest{}
			if cmd.Flags().Changed("title") {
				req.Title = &flags.title
			}
			if cmd.Flags().Changed("tag") {
				req.Tags = flags.tags
			}
			if cmd.Flags().Changed("content") {
				req.Content = &flags.content
			}

			n, err := deps.service.UpdateNote(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}

			if flags.jsonOut {
				printJSON(os.Stdout, n)
			} else {
				fmt.Printf("Updated note %s: %s\n", n.UUID, n.Title)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(
		&flags.title, "title", "", "New title")
	f.StringArrayVar(
		&flags.tags, "tag", nil, "New tags (repeatable)")
	f.StringVar(
		&flags.content, "content", "", "New content")
	f.BoolVar(
		&flags.jsonOut, "json", false, "Output as JSON")

	return cmd
}
