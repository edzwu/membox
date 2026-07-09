package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/earendil-works/membox/internal/app"
	"github.com/spf13/cobra"
)

func workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspace directories",
		Aliases: []string{"ws"},
	}
	cmd.AddCommand(workspaceAddCmd())
	cmd.AddCommand(workspaceLsCmd())
	cmd.AddCommand(workspaceRmCmd())
	return cmd
}

func workspaceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add \u003cdir\u003e",
		Short: "Add a notes directory to the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}

			a, err := app.New()
			if err != nil {
				return fmt.Errorf("load app: %w", err)
			}
			defer a.Close()

			if err := a.AddWorkspace(dir); err != nil {
				return fmt.Errorf("add workspace: %w", err)
			}
			fmt.Printf("Added workspace directory: %s\n", dir)
			return nil
		},
	}
}

func workspaceLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List workspace directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return fmt.Errorf("load app: %w", err)
			}
			defer a.Close()

			ws := a.Workspaces()
			if ws.IsEmpty() {
				fmt.Println("No workspace directories configured.")
				return nil
			}
			for _, dir := range ws.Notes {
				fmt.Println(dir)
			}
			return nil
		},
	}
}

func workspaceRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm \u003cdir\u003e",
		Short: "Remove a notes directory from the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			a, err := app.New()
			if err != nil {
				return fmt.Errorf("load app: %w", err)
			}
			defer a.Close()

			if err := a.RemoveWorkspace(dir); err != nil {
				return fmt.Errorf("remove workspace: %w", err)
			}
			fmt.Printf("Removed workspace directory: %s\n", dir)
			return nil
		},
	}
}
