package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Publisher stages generated blog files and commits/pushes them with the Git
// CLI, mirroring History's approach of deferring to the user's Git setup
// (credentials, hooks, signing) instead of linking a Git library.
type Publisher struct{}

// CommitAndPush stages paths relative to repoRoot, commits them with message
// when the staged tree differs, and pushes when push is true. It reports
// whether a commit was created; an unchanged tree is a no-op success.
func (Publisher) CommitAndPush(ctx context.Context, repoRoot, message string, paths []string, push bool) (bool, error) {
	if len(paths) == 0 {
		return false, fmt.Errorf("no paths to stage")
	}
	addArgs := append([]string{"-C", repoRoot, "add", "-A", "--"}, paths...)
	if output, err := run(ctx, addArgs...); err != nil {
		return false, fmt.Errorf("staging blog files: %s", output)
	}
	// diff --cached --quiet exits 1 when there are staged changes, 0 when clean.
	if _, err := run(ctx, "-C", repoRoot, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	if output, err := run(ctx, "-C", repoRoot, "commit", "--no-verify", "-m", message); err != nil {
		return false, fmt.Errorf("committing blog files: %s", output)
	}
	if !push {
		return true, nil
	}
	if output, err := run(ctx, "-C", repoRoot, "push"); err != nil {
		return true, fmt.Errorf("pushing blog commit: %s", output)
	}
	return true, nil
}

func run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
