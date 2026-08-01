package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
