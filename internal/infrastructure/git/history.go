package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application/port"
)

// History reads file dates from the Git CLI. Using the CLI keeps behavior
// consistent with the user's repository configuration and supports worktrees.
type History struct{}

func (History) RepositoryRoot(ctx context.Context, directory string) (string, bool, error) {
	command := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--show-toplevel")
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", false, ctxErr
		}
		var exitErr *exec.ExitError
		message := strings.TrimSpace(string(output))
		if errors.As(err, &exitErr) && strings.Contains(strings.ToLower(message), "not a git repository") {
			// A regular directory is an expected, non-applicable path rather
			// than a sync failure.
			return "", false, nil
		}
		if message != "" {
			return "", false, fmt.Errorf("finding Git repository for %q: %s", directory, message)
		}
		return "", false, fmt.Errorf("finding Git repository for %q: %w", directory, err)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", false, nil
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", false, fmt.Errorf("resolving Git root %q: %w", root, err)
	}
	return filepath.Clean(absolute), true, nil
}

func (History) FileTimes(ctx context.Context, repositoryRoot, absolutePath string) (port.RevisionTimes, bool, error) {
	relative, err := filepath.Rel(repositoryRoot, absolutePath)
	if err != nil {
		return port.RevisionTimes{}, false, fmt.Errorf("resolving Git path for %q: %w", absolutePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return port.RevisionTimes{}, false, fmt.Errorf("document %q is outside Git repository %q", absolutePath, repositoryRoot)
	}

	command := exec.CommandContext(ctx, "git", "-C", repositoryRoot, "log", "--follow", "--format=%aI", "--", filepath.ToSlash(relative))
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return port.RevisionTimes{}, false, ctxErr
		}
		message := strings.TrimSpace(string(output))
		if message != "" {
			return port.RevisionTimes{}, false, fmt.Errorf("reading Git history for %q: %s", absolutePath, message)
		}
		return port.RevisionTimes{}, false, fmt.Errorf("reading Git history for %q: %w", absolutePath, err)
	}
	lines := strings.Fields(string(output))
	if len(lines) == 0 {
		return port.RevisionTimes{}, false, nil
	}
	updatedAt, err := time.Parse(time.RFC3339, lines[0])
	if err != nil {
		return port.RevisionTimes{}, false, fmt.Errorf("parsing latest Git date for %q: %w", absolutePath, err)
	}
	createdAt, err := time.Parse(time.RFC3339, lines[len(lines)-1])
	if err != nil {
		return port.RevisionTimes{}, false, fmt.Errorf("parsing first Git date for %q: %w", absolutePath, err)
	}
	return port.RevisionTimes{CreatedAt: createdAt, UpdatedAt: updatedAt}, true, nil
}
