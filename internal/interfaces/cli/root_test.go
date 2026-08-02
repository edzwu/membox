package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"membox"
)

type fakeLauncher struct{}

func (fakeLauncher) EditorCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}
func (fakeLauncher) ViewerCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}
func (fakeLauncher) OpenCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}

func runTestCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, fakeLauncher{}, false)
	return code, stdout.String(), stderr.String()
}

func runGit(t *testing.T, directory string, env []string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), env...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestCLI_CLI002_MissingAddArgumentPrintsOnlyLocalUsage(t *testing.T) {
	code, stdout, stderr := runTestCLI(t, "path", "add")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout %q", stdout)
	}
	if !strings.Contains(stderr, "Usage:\n  mm path add <directory>") {
		t.Fatalf("missing local usage: %s", stderr)
	}
	for _, unrelated := range []string{"Find and operate on documents", "Inspect the document index", "Run interactive interfaces"} {
		if strings.Contains(stderr, unrelated) {
			t.Fatalf("included unrelated root help %q: %s", unrelated, stderr)
		}
	}
}

func TestCLI_CLI002_UnknownNestedCommandPrintsParentUsage(t *testing.T) {
	code, _, stderr := runTestCLI(t, "path", "wat")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, `unknown command "wat" for "mm path"`) || !strings.Contains(stderr, "mm path [command]") {
		t.Fatalf("wrong error: %s", stderr)
	}
	if strings.Contains(stderr, "Find and operate on documents") {
		t.Fatalf("printed root help: %s", stderr)
	}
}

func TestCLI_DOC001_ListCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "one.md"), []byte("# One\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "path", "add", directory); code != 0 {
		t.Fatalf("add failed: %d %s", code, stderr)
	}
	code, stdout, stderr := runTestCLI(t, "--home", home, "doc", "list")
	if code != 0 {
		t.Fatalf("list failed: %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "ID") || !strings.Contains(stdout, "One") || !strings.Contains(stdout, "active") {
		t.Fatalf("unexpected list output: %q", stdout)
	}
}

func TestCLI_PathScanGitTimestampForSpecificPathUsesHistoryAndFollowsRename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home, repository := filepath.Join(t.TempDir(), "home"), t.TempDir()
	runGit(t, repository, nil, "init", "--quiet")
	runGit(t, repository, nil, "config", "user.name", "Test")
	runGit(t, repository, nil, "config", "user.email", "test@example.com")

	oldPath := filepath.Join(repository, "old.md")
	if err := os.WriteFile(oldPath, []byte("# History\n\nfirst\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, nil, "add", "old.md")
	firstDate := "2020-01-02T03:04:05+00:00"
	runGit(t, repository, []string{"GIT_AUTHOR_DATE=" + firstDate, "GIT_COMMITTER_DATE=" + firstDate}, "commit", "--quiet", "-m", "add document")

	runGit(t, repository, nil, "mv", "old.md", "renamed.md")
	newPath := filepath.Join(repository, "renamed.md")
	if err := os.WriteFile(newPath, []byte("# History\n\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, nil, "add", "renamed.md")
	lastDate := "2021-06-07T08:09:10+00:00"
	runGit(t, repository, []string{"GIT_AUTHOR_DATE=" + lastDate, "GIT_COMMITTER_DATE=" + lastDate}, "commit", "--quiet", "-m", "rename and update")
	if err := os.WriteFile(filepath.Join(repository, "untracked.md"), []byte("# Untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := runTestCLI(t, "--home", home, "path", "add", repository); code != 0 {
		t.Fatalf("add failed: %d %s", code, stderr)
	}
	code, stdout, stderr := runTestCLI(t, "--home", home, "path", "scan", "1", "--timestamp=git", "--json")
	if code != 0 {
		t.Fatalf("scan with Git timestamps failed: %d %s", code, stderr)
	}
	var report membox.ScanReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.Paths != 1 || report.TimestampsUpdated != 1 || report.NoGitHistory != 1 || report.GitPaths != 1 {
		t.Fatalf("unexpected scan report: %+v", report)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "doc", "list", "--json")
	if code != 0 {
		t.Fatalf("list failed: %d %s", code, stderr)
	}
	var documents []membox.DocumentView
	if err := json.Unmarshal([]byte(stdout), &documents); err != nil {
		t.Fatal(err)
	}
	var history membox.DocumentView
	for _, document := range documents {
		if document.Title == "History" {
			history = document
		}
	}
	wantCreated, _ := time.Parse(time.RFC3339, firstDate)
	wantUpdated, _ := time.Parse(time.RFC3339, lastDate)
	if !history.CreatedAt.Equal(wantCreated) || !history.UpdatedAt.Equal(wantUpdated) {
		t.Fatalf("Git dates not applied across rename: created=%s updated=%s", history.CreatedAt, history.UpdatedAt)
	}

	// A normal no-op filesystem scan must not overwrite synced historical dates.
	if code, _, stderr := runTestCLI(t, "--home", home, "path", "scan"); code != 0 {
		t.Fatalf("scan failed: %d %s", code, stderr)
	}
	code, stdout, stderr = runTestCLI(t, "--home", home, "path", "scan", "1", "--timestamp=git", "--json")
	if code != 0 {
		t.Fatalf("second scan with Git timestamps failed: %d %s", code, stderr)
	}
	report = membox.ScanReport{}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.TimestampsUpdated != 0 || report.TimestampsUnchanged != 1 {
		t.Fatalf("Git timestamp scan is not idempotent: %+v", report)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "doc", "show", history.ID, "--json")
	if code != 0 {
		t.Fatalf("show failed: %d %s", code, stderr)
	}
	var afterScan membox.DocumentView
	if err := json.Unmarshal([]byte(stdout), &afterScan); err != nil {
		t.Fatal(err)
	}
	if !afterScan.CreatedAt.Equal(wantCreated) || !afterScan.UpdatedAt.Equal(wantUpdated) {
		t.Fatalf("scan overwrote Git dates: created=%s updated=%s", afterScan.CreatedAt, afterScan.UpdatedAt)
	}
}

func TestCLI_CLI002_InvalidLeafFlagPrintsLeafUsage(t *testing.T) {
	code, _, stderr := runTestCLI(t, "doc", "search", "query", "--limit", "not-a-number")
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "Usage:\n  mm doc search <query>") {
		t.Fatalf("missing leaf usage: %s", stderr)
	}
	if strings.Contains(stderr, "Manage Markdown scan paths") {
		t.Fatalf("printed root help: %s", stderr)
	}
}

func TestCLI_CLI002_RuntimeErrorDoesNotPrintUsage(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, _, stderr := runTestCLI(t, "--home", home, "doc", "show", "deadbeef")
	if code != 1 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Fatalf("runtime error printed usage: %s", stderr)
	}
	if !strings.Contains(stderr, "not found") {
		t.Fatalf("wrong runtime error: %s", stderr)
	}
}

func TestCLI_TUI001_NonTTYWithoutCommandShowsRootUsage(t *testing.T) {
	code, _, stderr := runTestCLI(t)
	if code != 2 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "a command is required") || !strings.Contains(stderr, "Usage:\n  mm") {
		t.Fatalf("wrong output: %s", stderr)
	}
}
