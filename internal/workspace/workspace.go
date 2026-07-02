// Package workspace manages the set of notes directories that make up a
// membox workspace. The workspace definition is persisted as JSON under the
// runtime data directory (e.g. .membox/data/workspaces.json in dev mode).
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/earendil-works/membox/internal/config"
)

const fileName = "workspaces.json"

// Workspace describes the directories that belong to a membox workspace.
// Markdown files in these directories are the ground truth for note content;
// SQLite holds only metadata and search indexes.
type Workspace struct {
	// Notes is a list of absolute paths to directories containing markdown
	// note files. The first directory is the default write target for new
	// notes. An empty list means the workspace is uninitialized.
	Notes []string `json:"notes"`
}

// Load reads the workspace configuration from the data directory. If the file
// does not exist yet, an empty workspace is returned so the caller can decide
// how to onboard the user.
func Load(cfg *config.Config) (*Workspace, error) {
	path := filepath.Join(cfg.DataDir, fileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Workspace{Notes: nil}, nil
		}
		return nil, fmt.Errorf("read workspace file %q: %w", path, err)
	}

	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parse workspace file %q: %w", path, err)
	}

	// Normalize paths to absolute form so downstream code does not have to
	// worry about relative directories or symlinks.
	for i, dir := range ws.Notes {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, fmt.Errorf("resolve notes dir %q: %w", dir, err)
		}
		ws.Notes[i] = abs
	}

	if err := ws.validate(); err != nil {
		return nil, err
	}

	return &ws, nil
}

// Save persists the workspace to the data directory, creating the directory
// if necessary.
func (w *Workspace) Save(cfg *config.Config) error {
	if err := w.validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	path := filepath.Join(cfg.DataDir, fileName)
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write workspace file %q: %w", path, err)
	}
	return nil
}

// IsEmpty reports whether the workspace has no notes directories configured.
func (w *Workspace) IsEmpty() bool {
	return len(w.Notes) == 0
}

// Add appends a notes directory to the workspace if it is not already present.
func (w *Workspace) Add(dir string) error {
	if dir == "" {
		return fmt.Errorf("notes directory cannot be empty")
	}
	for _, existing := range w.Notes {
		if existing == dir {
			return fmt.Errorf("notes directory already in workspace: %q", dir)
		}
	}
	w.Notes = append(w.Notes, dir)
	return nil
}

// Remove deletes a notes directory from the workspace.
func (w *Workspace) Remove(dir string) error {
	for i, existing := range w.Notes {
		if existing == dir {
			w.Notes = append(w.Notes[:i], w.Notes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("notes directory not in workspace: %q", dir)
}

func (w *Workspace) validate() error {
	seen := make(map[string]struct{}, len(w.Notes))
	for _, dir := range w.Notes {
		if dir == "" {
			return fmt.Errorf("workspace notes directory cannot be empty")
		}
		if _, ok := seen[dir]; ok {
			return fmt.Errorf("duplicate notes directory in workspace: %q", dir)
		}
		seen[dir] = struct{}{}
	}
	return nil
}
