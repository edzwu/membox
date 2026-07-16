package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DiscoverDefaultNotesDir returns an auto-detected default notes directory.
// Resolution order:
//  1. $MM_NOTES_DIR if set
//  2. If MM_DEV=1, ./notes under the current working directory
//  3. <git-root>/notes if inside a git repository
//  4. ~/notes
//  5. ~/.local/share/mm/notes
//
// The returned path is absolute. The directory may not exist yet.
func DiscoverDefaultNotesDir() (string, error) {
	if env := os.Getenv("MM_NOTES_DIR"); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", fmt.Errorf("resolve MM_NOTES_DIR: %w", err)
		}
		return abs, nil
	}

	if os.Getenv("MM_DEV") == "1" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		return filepath.Join(cwd, "notes"), nil
	}

	if gitRoot, err := gitRoot(); err == nil && gitRoot != "" {
		return filepath.Join(gitRoot, "notes"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, "notes"), nil
}

func gitRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
