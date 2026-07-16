// Package app is the application layer that orchestrates membox use cases.
//
// It sits above the service and repository layers and is responsible for:
//   - resolving runtime directories (config, data, workspace)
//   - opening the SQLite metadata store
//   - keeping the filesystem-to-database index in sync
//   - exposing high-level operations used by both the CLI and the TUI
//
// The TUI and CLI are both thin clients over this layer.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/earendil-works/membox/internal/config"
	"github.com/earendil-works/membox/internal/domain"
	fsrepo "github.com/earendil-works/membox/internal/repository/filesystem"
	"github.com/earendil-works/membox/internal/repository"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
	"github.com/earendil-works/membox/internal/service"
	"github.com/earendil-works/membox/internal/storage/sqlite"
	"github.com/earendil-works/membox/internal/workspace"
)

// App orchestrates membox runtime state for a single workspace session.
type App struct {
	cfg     *config.Config
	ws      *workspace.Workspace
	db      *sqlite.DB
	service *service.NoteService
}

// New loads configuration, opens the database, and initializes the workspace.
// It does not perform a sync; callers should call Sync explicitly.
func New() (*App, error) {
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

	if ws.IsEmpty() {
		defaultDir, err := workspace.DiscoverDefaultNotesDir()
		if err != nil {
			return nil, fmt.Errorf("discover default notes dir: %w", err)
		}
		if err := os.MkdirAll(defaultDir, 0o755); err != nil {
			return nil, fmt.Errorf("create default notes dir: %w", err)
		}
		if err := ws.Add(defaultDir); err != nil {
			return nil, fmt.Errorf("add default workspace: %w", err)
		}
		if err := ws.Save(cfg); err != nil {
			return nil, fmt.Errorf("save default workspace: %w", err)
		}
	}

	db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	repo := sqliterepo.NewNoteMetaRepo(db.DB())
	store := fsrepo.NewNoteStore(ws.Notes)

	return &App{
		cfg:     cfg,
		ws:      ws,
		db:      db,
		service: service.NewNoteService(repo, store, os.Getenv("EDITOR")),
	}, nil
}

// Close releases application resources.
func (a *App) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// Sync reconciles the SQLite metadata index with all filesystem notes directories.
// If the workspace is empty, it returns an empty result and no error.
func (a *App) Sync(ctx context.Context) (*service.SyncResult, error) {
	if a.ws.IsEmpty() {
		return &service.SyncResult{}, nil
	}
	return a.service.Sync(ctx)
}

// ListNotes returns recent notes from all workspace directories.
// If the workspace is empty, it returns an empty slice and no error.
func (a *App) ListNotes(ctx context.Context, includeTags, excludeTags []string, limit int) ([]*domain.Note, error) {
	if a.ws.IsEmpty() {
		return []*domain.Note{}, nil
	}
	return a.service.ListNotes(ctx, includeTags, excludeTags, limit)
}

// SearchNotes performs a full-text search over note titles.
// If the workspace is empty, it returns an empty slice and no error.
func (a *App) SearchNotes(ctx context.Context, query string, limit int) ([]*repository.NoteSearchResult, error) {
	if a.ws.IsEmpty() {
		return []*repository.NoteSearchResult{}, nil
	}
	return a.service.SearchNotes(ctx, query, limit)
}

// Workspaces returns the current workspace configuration.
func (a *App) Workspaces() *workspace.Workspace {
	return a.ws
}

// AddWorkspace adds a notes directory to the workspace and persists the configuration.
func (a *App) AddWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", dir, err)
	}
	if err := a.ws.Add(abs); err != nil {
		return err
	}
	if err := a.ws.Save(a.cfg); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	// Recreate the note store so subsequent operations see the new directory.
	a.service.SetStore(fsrepo.NewNoteStore(a.ws.Notes))
	return nil
}

// RemoveWorkspace removes a notes directory from the workspace.
func (a *App) RemoveWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", dir, err)
	}
	if err := a.ws.Remove(abs); err != nil {
		return err
	}
	if err := a.ws.Save(a.cfg); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	a.service.SetStore(fsrepo.NewNoteStore(a.ws.Notes))
	return nil
}
