package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/earendil-works/membox/internal/domain"
)

func noteListCmd() *cobra.Command {
	var flags struct {
		exclude []string
		all     bool
		json    bool
		limit   int
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List recent notes",
		Long: `List the most recently updated notes, optionally filtered by tags.

Filter syntax:
  mm note ls                 # all recent notes
  mm note ls +work           # notes tagged with 'work'
  mm note ls +work +urgent   # notes tagged with both 'work' and 'urgent'
  mm note ls +work --exclude done   # notes tagged 'work' but not 'done'
  mm note ls --all           # include creation date and tags columns`,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeTags, err := parseIncludeTags(args)
			if err != nil {
				return err
			}

			deps, err := newNoteDeps()
			if err != nil {
				return err
			}
			defer deps.Close()

			notes, err := deps.service.ListNotes(cmd.Context(), includeTags, flags.exclude, flags.limit)
			if err != nil {
				return err
			}

			if flags.json {
				printJSON(os.Stdout, notes)
				return nil
			}

			if len(notes) == 0 {
				fmt.Println("No notes found.")
				return nil
			}

			printNoteList(os.Stdout, notes, flags.all)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringSliceVar(&flags.exclude, "exclude", nil, "Exclude notes tagged with this value (may be repeated)")
	f.BoolVar(&flags.all, "all", false, "Include creation date and tags columns")
	f.BoolVar(&flags.json, "json", false, "Output as JSON")
	f.IntVar(&flags.limit, "limit", 20, "Maximum number of notes")

	return cmd
}

// parseIncludeTags extracts +tag positional arguments.
func parseIncludeTags(args []string) ([]string, error) {
	var include []string
	for _, a := range args {
		if !strings.HasPrefix(a, "+") {
			return nil, fmt.Errorf("invalid filter %q: use +tag", a)
		}
		include = append(include, strings.TrimPrefix(a, "+"))
	}
	return include, nil
}

// printNoteList prints note rows with a compact or full layout.
func printNoteList(w io.Writer, notes []*domain.Note, all bool) {
	if all {
		fmt.Fprintln(w, "UUID     Created      Updated            Title")
		for _, n := range notes {
			tagStr := ""
			if len(n.Tags) > 0 {
				tagStr = " " + strings.Join(n.Tags, " ")
			}
			fmt.Fprintf(w, "%s  %s  %s  %s%s\n",
				shortUUID(n.UUID),
				n.CreatedAt.Format(createDateFmt),
				n.UpdatedAt.Format(updateTimeFmt),
				n.Title,
				tagStr,
			)
		}
		return
	}

	fmt.Fprintln(w, "UUID     Updated            Title")
	for _, n := range notes {
		fmt.Fprintf(w, "%s  %s  %s\n",
			shortUUID(n.UUID),
			n.UpdatedAt.Format(updateTimeFmt),
			n.Title,
		)
	}
}
