package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/earendil-works/membox/internal/config"
	"github.com/earendil-works/membox/internal/perkeep"
	fsrepo "github.com/earendil-works/membox/internal/repository/filesystem"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
	"github.com/earendil-works/membox/internal/service"
	"github.com/earendil-works/membox/internal/storage/sqlite"
	"github.com/earendil-works/membox/internal/workspace"
	"github.com/spf13/cobra"
)

func perkeepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "perkeep",
		Short: "Sync notes with a Perkeep server",
		Long:  `Push local notes to a Perkeep server and inspect sync state.`,
	}
	cmd.AddCommand(perkeepPushCmd())
	cmd.AddCommand(perkeepStatusCmd())
	return cmd
}

func perkeepPushCmd() *cobra.Command {
	var flags struct {
		verbose bool
		server  string
	}

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push changed notes to Perkeep",
		Long: `Upload notes that have changed since the last sync to the Perkeep
server configured in ~/.config/perkeep/client-config.json.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := config.InitWorkspace(cfg); err != nil {
				return fmt.Errorf("init workspace: %w", err)
			}

			ws, err := workspace.Load(cfg)
			if err != nil {
				return fmt.Errorf("load workspace: %w", err)
			}
			if err := ws.Save(cfg); err != nil {
				return fmt.Errorf("save workspace: %w", err)
			}

			db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			repo := sqliterepo.NewNoteMetaRepo(db.DB())
			syncRepo := sqliterepo.NewPerkeepSyncRepo(db.DB())
			store := fsrepo.NewNoteStore(ws.Notes)
			svc := service.NewNoteService(repo, store, os.Getenv("EDITOR"))

			// Ensure the local metadata index reflects the filesystem before
			// computing content hashes and pushing to Perkeep.
			if _, err := svc.Sync(cmd.Context()); err != nil {
				return fmt.Errorf("sync metadata: %w", err)
			}

			client := perkeep.NewClient()
			if flags.server != "" {
				client = perkeep.NewClientWithServer(flags.server)
			}
			syncer := perkeep.NewSyncer(client, store, repo, syncRepo, filepath.Join(cfg.DataDir, "perkeep-cache"))
			res, err := syncer.Push(cmd.Context())
			if err != nil {
				return fmt.Errorf("sync to perkeep: %w", err)
			}

			fmt.Printf("Pushed %d notes, skipped %d, failed %d\n", res.Uploaded, res.Skipped, res.Failed)
			if flags.verbose {
				for _, e := range res.Errors {
					fmt.Fprintf(os.Stderr, "  error: %v\n", e)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flags.verbose, "verbose", false, "Print per-note errors")
	cmd.Flags().StringVar(&flags.server, "server", "", "Override Perkeep server prefix (e.g. http://127.0.0.1:3179)")
	return cmd
}

func perkeepStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Perkeep sync status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := config.InitWorkspace(cfg); err != nil {
				return fmt.Errorf("init workspace: %w", err)
			}

			db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			syncRepo := sqliterepo.NewPerkeepSyncRepo(db.DB())
			recs, err := syncRepo.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list sync records: %w", err)
			}
			if len(recs) == 0 {
				fmt.Println("No notes synced to Perkeep yet.")
				return nil
			}
			fmt.Printf("%d notes synced to Perkeep:\n", len(recs))
			for _, r := range recs {
				fmt.Printf("  %s  %s  %s  %s\n", r.UUID, r.Permanode, r.ContentHash[:12], r.SyncedAt)
			}
			return nil
		},
	}
	return cmd
}
