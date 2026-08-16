package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"membox"
)

func newDocRewriteCommand(runtime *runtime) *cobra.Command {
	var (
		model      string
		hints      []string
		hintFile   string
		dryRun     bool
		jsonOutput bool
	)
	command := &cobra.Command{
		Use:   "rewrite <document-id>",
		Short: "Rewrite Markdown for readability with an LLM",
		Long: `Improve a Markdown document's readability (PDF conversion debris, broken
headings, garbled code fences, etc.) via LLM completion, then write the
result back in place (preserving UUID).

Large documents are rewritten in chunks so the model never sees the whole
file at once: prefer heading sections; if there is no outline, pack by
paragraphs / fenced code / tables; oversized blobs hard-split by rune budget.
Local default ~5.5k runes/chunk; deepseek ~20k. Front matter is kept as-is.

Continuity: each chunk sees a short tail of the previous rewrite; after all
chunks, a light second pass polishes each join (seam) so cuts read naturally.

Default model is local qwen3:14b through mmd (same stack as translation /
PDF structure planning). Pass --model deepseek to use deepseek-v4-flash.

Extra guidance for the model (especially useful with the local model):
  --hint '…'           repeatable; injected into every chunk prompt
  --hint-file path     load one more hint block from a file

Examples:
  mm doc rewrite 01a00a54
  mm doc rewrite 01a00a54 --model deepseek
  mm doc rewrite 01a00a54 --model local \
    --hint 'C/C++ 代码围栏必须用 cpp，不要用 txt/c'
  mm doc rewrite 01a00a54 --hint-file ./rewrite-notes.txt --dry-run
`,
		Args: exactArgs(1, "document ID"),
	}
	command.Flags().StringVar(&model, "model", "", "local (default qwen3:14b) | deepseek | provider/model")
	command.Flags().StringArrayVar(&hints, "hint", nil, "extra instruction for the model (repeatable)")
	command.Flags().StringVar(&hintFile, "hint-file", "", "read extra instruction from a file")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print rewritten Markdown without writing the file")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		model = strings.TrimSpace(model)
		label := "local qwen3:14b"
		switch strings.ToLower(model) {
		case "", "local", "qwen", "qwen3", "qwen3:14b", "ollama":
			// default
		case "deepseek", "deepseek-flash", "flash", "deepseek/deepseek-v4-flash":
			label = "deepseek/deepseek-v4-flash"
		default:
			label = model
		}
		hint, err := joinRewriteHints(hints, hintFile)
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Fprintf(cmd.ErrOrStderr(), "Rewriting with %s (dry-run)…\n", label)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Rewriting with %s…\n", label)
		}
		if hint != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "  hint: %s\n", summarizeHint(hint))
		}
		result, err := box.RewriteDocument(cmd.Context(), membox.RewriteDocumentCommand{
			Selector: args[0],
			Model:    model,
			Hint:     hint,
			DryRun:   dryRun,
			OnProgress: func(p membox.RewriteProgress) {
				if p.Total <= 1 {
					return
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "  [%d/%d] %s\n", p.Done, p.Total, p.Detail)
			},
		})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if dryRun {
			_, err := cmd.OutOrStdout().Write([]byte(result.Markdown))
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Rewrote %s (%s) with %s\n  chunks: %d (≤%d runes each)\n  in:  %d bytes\n  out: %d bytes\n  path: %s\n",
			shortID(result.DocumentID), result.Title, result.Label, result.Chunks, result.ChunkRunes, result.BytesIn, result.BytesOut, result.Path)
		return nil
	}
	return command
}

func joinRewriteHints(flags []string, file string) (string, error) {
	var parts []string
	for _, h := range flags {
		h = strings.TrimSpace(h)
		if h != "" {
			parts = append(parts, h)
		}
	}
	file = strings.TrimSpace(file)
	if file != "" {
		body, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading --hint-file: %w", err)
		}
		text := strings.TrimSpace(string(body))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), nil
}

func summarizeHint(hint string) string {
	hint = strings.Join(strings.Fields(hint), " ")
	const max = 96
	if len([]rune(hint)) <= max {
		return hint
	}
	r := []rune(hint)
	return string(r[:max]) + "…"
}
