package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"membox"
)

func newBlogCommand(runtime *runtime) *cobra.Command {
	blog := parentCommand("blog", "Manage the public blog", "a blog command is required")
	blog.AddCommand(
		newBlogPublishCommand(runtime),
		newBlogUnpublishCommand(runtime),
		newBlogListCommand(runtime),
		newBlogShowCommand(runtime),
		newBlogSyncCommand(runtime),
		newBlogStatusCommand(runtime),
	)
	return blog
}

func newBlogPublishCommand(runtime *runtime) *cobra.Command {
	var lang, slug string
	var noPush, wait, jsonOutput bool
	command := &cobra.Command{
		Use:   "publish <document-id>",
		Short: "Publish a document to the blog (or update its public copy)",
		Args:  exactArgs(1, "document ID"),
	}
	command.Flags().StringVar(&lang, "lang", "", "page language: en or zh (default: en, or keep the published language)")
	command.Flags().StringVar(&slug, "slug", "", "URL slug (default: the document's short ID, or keep the published slug)")
	command.Flags().BoolVar(&noPush, "no-push", false, "commit locally without pushing")
	command.Flags().BoolVar(&wait, "wait", false, "watch the deploy until the page is live")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.PublishDocument(cmd.Context(), membox.PublishCommand{
			Selector: args[0], Lang: lang, Slug: slug, NoPush: noPush,
		})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, view)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Published %s as %s\n", view.ID, view.URL)
		if noPush {
			fmt.Fprintln(out, "Committed locally; push to deploy.")
			return nil
		}
		if !wait {
			fmt.Fprintln(out, "Pushed; track the deploy with: mm blog status")
			return nil
		}
		return watchDeploy(cmd, box, args[0])
	}
	return command
}

func newBlogUnpublishCommand(runtime *runtime) *cobra.Command {
	var noPush, jsonOutput bool
	command := &cobra.Command{
		Use:   "unpublish <document-id>",
		Short: "Remove a document from the blog",
		Args:  exactArgs(1, "document ID"),
	}
	command.Flags().BoolVar(&noPush, "no-push", false, "commit locally without pushing")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.UnpublishDocument(cmd.Context(), args[0], noPush)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, view)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Unpublished %s\n", view.URL)
		return nil
	}
	return command
}

func newBlogListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List published documents", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		views, err := box.ListPublications(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, views)
		}
		out := cmd.OutOrStdout()
		if len(views) == 0 {
			fmt.Fprintln(out, "No published documents. Publish one with: mm blog publish <document-id>")
			return nil
		}
		stale := 0
		for _, view := range views {
			marker := ""
			if view.Status == "stale" {
				marker = "  [stale: source changed, run mm blog sync]"
				stale++
			}
			fmt.Fprintf(out, "%-12s %-3s %-40s %s%s\n",
				view.Slug, view.Lang, displayName(view.Title, ""), view.PublishedAt.Local().Format("2006-01-02"), marker)
		}
		fmt.Fprintf(out, "%d published, %d stale.\n", len(views), stale)
		return nil
	}
	return command
}

func newBlogShowCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "show <document-id>",
		Short: "Show one document's publication details",
		Args:  exactArgs(1, "document ID"),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.GetPublication(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, view)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "document:  %s (%s)\n", view.ID, view.Title)
		fmt.Fprintf(out, "url:       %s\n", view.URL)
		fmt.Fprintf(out, "status:    %s\n", view.Status)
		fmt.Fprintf(out, "lang:      %s\n", view.Lang)
		fmt.Fprintf(out, "slug:      %s\n", view.Slug)
		fmt.Fprintf(out, "published: %s\n", view.PublishedAt.Local().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(out, "bundle:    %s\n", view.BundlePath)
		if view.Status == "stale" {
			fmt.Fprintln(out, "Source changed since publish; run: mm blog sync")
		}
		return nil
	}
	return command
}

func newBlogStatusCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "status", Short: "Show deploy pipeline state (CI run + live URLs)", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		view, err := box.BlogStatus(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, view)
		}
		out := cmd.OutOrStdout()
		if view.RunKnown {
			conclusion := view.RunConclusion
			if conclusion == "" {
				conclusion = "..."
			}
			fmt.Fprintf(out, "CI: %s (%s)\n  %s\n  %s\n", view.RunStatus, conclusion, view.RunTitle, view.RunURL)
		} else {
			fmt.Fprintln(out, "CI: state unavailable (install gh or push first)")
		}
		if len(view.Publications) == 0 {
			fmt.Fprintln(out, "No published documents.")
			return nil
		}
		for _, item := range view.Publications {
			state := fmt.Sprintf("HTTP %d", item.HTTPStatus)
			if item.Live {
				state = "live"
			} else if item.HTTPStatus == 0 {
				state = "unreachable"
			}
			fmt.Fprintf(out, "%-12s %-12s %s\n", item.Slug, state, item.URL)
		}
		return nil
	}
	return command
}

// watchDeploy renders a spinner while waiting for the page to go live.
func watchDeploy(cmd *cobra.Command, box *membox.Box, selector string) error {
	out := cmd.OutOrStdout()
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	tick := 0
	last := ""
	ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Minute)
	defer cancel()
	err := box.WatchDeploy(ctx, selector, func(update membox.DeployProgressView) {
		frame := frames[tick%len(frames)]
		tick++
		last = fmt.Sprintf("%s [%s] %s (%ds)", frame, update.Phase, update.Message, int(update.Elapsed.Seconds()))
		fmt.Fprintf(out, "\r%-72s", last)
	})
	fmt.Fprintln(out)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ %s\n", last)
	return nil
}

func newBlogSyncCommand(runtime *runtime) *cobra.Command {
	var noPush, jsonOutput bool
	command := &cobra.Command{Use: "sync", Short: "Re-export stale published documents", Args: noArgs}
	command.Flags().BoolVar(&noPush, "no-push", false, "commit locally without pushing")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		updated, err := box.SyncPublications(cmd.Context(), noPush)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, map[string]int{"updated": updated})
		}
		out := cmd.OutOrStdout()
		if updated == 0 {
			fmt.Fprintln(out, "All publications are up to date.")
		} else {
			fmt.Fprintf(out, "Synced %d publication(s).\n", updated)
		}
		return nil
	}
	return command
}
