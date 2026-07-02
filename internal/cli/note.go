package cli

import (
	"database/sql"
	"fmt"
	"context"
	"os"
	"path/filepath"

	"github.com/earendil-works/membox/internal/config"
	fsrepo "github.com/earendil-works/membox/internal/repository/filesystem"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
	"github.com/earendil-works/membox/internal/service"
	"github.com/earendil-works/membox/internal/storage/sqlite"
	"github.com/earendil-works/membox/internal/workspace"
	"github.com/spf13/cobra"
)

func noteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Manage markdown notes",
		Long:  `Create, read, update, delete, and search markdown notes stored as UUID-backed documents.`,
		Aliases: []string{"n"},
	}

	cmd.AddCommand(noteNewCmd())
	cmd.AddCommand(noteListCmd())
	cmd.AddCommand(noteGetCmd())
	cmd.AddCommand(noteUpdateCmd())
	cmd.AddCommand(noteDeleteCmd())
	cmd.AddCommand(noteRenameCmd())
	cmd.AddCommand(noteSearchCmd())
	cmd.AddCommand(noteSyncCmd())
	cmd.AddCommand(openCmd())

	return cmd
}

// noteDeps wires the note service for CLI commands.
// Later this will become an HTTP/gRPC client when a daemon is running.
type noteDeps struct {
	db      *sql.DB
	service *service.NoteService
}

func newNoteDeps() (*noteDeps, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := config.InitWorkspace(cfg); err != nil {
		return nil, fmt.Errorf("init workspace: %w", err)
	}

	ws, err := workspace.Load(cfg)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	if err := ws.Save(cfg); err != nil {
		return nil, fmt.Errorf("save workspace: %w", err)
	}

	editor := os.Getenv("EDITOR")

	db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	repo := sqliterepo.NewNoteMetaRepo(db.DB())
	store := fsrepo.NewNoteStore(ws.Notes)
	deps := &noteDeps{
		db:      db.DB(),
		service: service.NewNoteService(repo, store, editor),
	}

	// Keep the SQLite metadata index in sync with the filesystem every time
	// the CLI initializes. This ensures list/search see external edits without
	// requiring an explicit sync command.
	if _, err := deps.service.Sync(context.Background()); err != nil {
		deps.Close()
		return nil, fmt.Errorf("sync notes: %w", err)
	}

	return deps, nil
}

func (d *noteDeps) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// isInteractive returns true when stdin is a terminal and no content is piped.
func isInteractive() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
