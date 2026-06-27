package cli

import (
	"fmt"
	"path/filepath"

	"github.com/earendil-works/membox/internal/config"
	"github.com/earendil-works/membox/internal/storage/sqlite"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize membox runtime directories",
		Long: `Ensure the membox runtime directories and database exist.

In dev mode (MM_DEV=1) config and data live under ./.membox in the current
working directory. Otherwise XDG base directories are used (~/.config/mm/ and
~/.local/share/mm/ by default).

Dev mode is the recommended way to start using membox in a project or
knowledge-base directory. Once initialized, running mm with MM_DEV=1 from this
directory (or any subdirectory) will use the workspace-local paths under .membox/.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.InitWorkspace(cfg); err != nil {
				return err
			}
			db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
			if err != nil {
				return fmt.Errorf("create database: %w", err)
			}
			if err := db.Close(); err != nil {
				return fmt.Errorf("close database: %w", err)
			}

			fmt.Printf("Initialized membox config dir: %s\n", cfg.ConfigDir)
			fmt.Printf("Initialized membox data dir:   %s\n", cfg.DataDir)
			return nil
		},
	}
}
