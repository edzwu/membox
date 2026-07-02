package cli

import (
	"fmt"
	"os"

	"github.com/earendil-works/membox/internal/service"
	"github.com/spf13/cobra"
)

func noteNewCmd() *cobra.Command {
	var flags struct {
		tags     []string
		content  string
		jsonOut  bool
		skipEdit bool
	}

	cmd := &cobra.Command{
		Use:   "new [flags] <title>...",
		Short: "Create a new markdown note",
		Long: `Create a new markdown note backed by a UUID.

The note is saved as a markdown file and its metadata is indexed in SQLite.
If no --content is provided and stdin is a TTY, the editor configured by the
EDITOR environment variable (default: vim) opens the new note.

Use --tag to organize notes; tags are stored in SQLite metadata and can be
used to filter the list view.

Examples:
  mm note new "KMP 学习笔记"
  mm note new "KMP 学习笔记" --tag algorithm --tag string
  mm note new "KMP 学习笔记" --content "前缀函数..."
  echo "前缀函数..." | mm note new "KMP 学习笔记"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			title := joinArgs(args)
			content, err := readContentOrStdin(flags.content)
			if err != nil {
				return err
			}

			interactive := !flags.skipEdit && content == "" && isInteractive()
			n, err := deps.service.CreateNote(cmd.Context(), service.CreateNoteRequest{
				Title:   title,
				Tags:    flags.tags,
				Content: content,
			}, interactive)
			if err != nil {
				return err
			}

			if flags.jsonOut {
				printJSON(os.Stdout, n)
			} else {
				fmt.Printf("Created note %s: %s\n", n.UUID, n.Title)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringArrayVar(&flags.tags, "tag", nil, "Tag (repeatable)")
	f.StringVar(&flags.content, "content", "", "Note content (reads stdin if omitted)")
	f.BoolVar(&flags.jsonOut, "json", false, "Output as JSON")
	f.BoolVar(&flags.skipEdit, "no-edit", false, "Skip editor even in TTY")

	return cmd
}
