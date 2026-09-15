package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"membox/internal/application/port"
)

// ErrGHUnavailable means the gh CLI is not installed; deploy watching falls
// back to plain URL polling in that case.
var ErrGHUnavailable = errors.New("gh CLI is not available")

// DeployWatcher reads GitHub Actions state through the gh CLI, reusing the
// user's existing gh authentication (required for private repos).
type DeployWatcher struct{}

type ghRun struct {
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	URL          string    `json:"url"`
	DisplayTitle string    `json:"displayTitle"`
	HeadSHA      string    `json:"headSha"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// LatestDeployRun returns the most recent run of the deploy workflow for the
// repository containing dir (gh infers owner/repo from the git remote).
func (DeployWatcher) LatestDeployRun(ctx context.Context, dir string) (port.DeployRun, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return port.DeployRun{}, ErrGHUnavailable
	}
	command := exec.CommandContext(ctx, "gh", "run", "list",
		"--workflow", "deploy-blog.yml", "--limit", "1",
		"--json", "status,conclusion,url,displayTitle,headSha,updatedAt")
	command.Dir = dir
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return port.DeployRun{}, fmt.Errorf("querying deploy runs: %s", string(output))
	}
	var runs []ghRun
	if err := json.Unmarshal(output, &runs); err != nil {
		return port.DeployRun{}, fmt.Errorf("parsing deploy runs: %w", err)
	}
	if len(runs) == 0 {
		return port.DeployRun{}, nil
	}
	run := runs[0]
	return port.DeployRun{
		Status:     run.Status,
		Conclusion: run.Conclusion,
		URL:        run.URL,
		Title:      run.DisplayTitle,
		SHA:        run.HeadSHA,
		UpdatedAt:  run.UpdatedAt,
	}, nil
}
