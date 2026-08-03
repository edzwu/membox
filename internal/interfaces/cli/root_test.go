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

func TestCLI_NoteNewCreatesDocumentAndManualLink(t *testing.T) {
	home, notes := filepath.Join(t.TempDir(), "home"), t.TempDir()
	sourcePath := filepath.Join(notes, "source.md")
	if err := os.WriteFile(sourcePath, []byte("# Source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "path", "add", notes); code != 0 {
		t.Fatalf("add failed: %d %s", code, stderr)
	}
	code, stdout, stderr := runTestCLI(t, "--home", home, "doc", "list", "--json")
	if code != 0 {
		t.Fatalf("list failed: %d %s", code, stderr)
	}
	var documents []membox.DocumentView
	if err := json.Unmarshal([]byte(stdout), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents=%d", len(documents))
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "note", "new", "Online Softmax Intuition", "--from", documents[0].ID, "--json", "--no-open")
	if code != 0 {
		t.Fatalf("note new failed: %d %s", code, stderr)
	}
	var created membox.CreateNoteResult
	if err := json.Unmarshal([]byte(stdout), &created); err != nil {
		t.Fatal(err)
	}
	if created.Document.ID == "" || created.Document.Path == "" || created.Link == nil || created.Link.FromDocumentID != documents[0].ID || created.Link.ToDocumentID != created.Document.ID {
		t.Fatalf("unexpected note result: %+v", created)
	}
	body, err := os.ReadFile(created.Document.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "# Online Softmax Intuition") {
		t.Fatalf("note content=%q", body)
	}

	if code, _, stderr := runTestCLI(t, "--home", home, "note", "new", "Online Softmax Intuition", "--from", documents[0].ID, "--json", "--no-open"); code != 0 {
		t.Fatalf("duplicate note new failed: %d %s", code, stderr)
	}
	entries, err := filepath.Glob(filepath.Join(notes, "online-softmax-intuition*.md"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected unique note filenames: entries=%v err=%v", entries, err)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "link", "list", created.Document.ID, "--json")
	if code != 0 {
		t.Fatalf("links failed: %d %s", code, stderr)
	}
	var graph membox.DocumentGraphView
	if err := json.Unmarshal([]byte(stdout), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Incoming) != 1 || graph.Incoming[0].ID != documents[0].ID {
		t.Fatalf("new note does not have source backlink: %+v", graph)
	}
}

func TestCLI_DocumentGraphSupportsManualLinksAndTopicMembership(t *testing.T) {
	home, notes := filepath.Join(t.TempDir(), "home"), t.TempDir()
	if err := os.WriteFile(filepath.Join(notes, "alpha.md"), []byte("# Alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "beta.md"), []byte("# Beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "path", "add", notes); code != 0 {
		t.Fatalf("add failed: %d %s", code, stderr)
	}
	code, stdout, stderr := runTestCLI(t, "--home", home, "doc", "list", "--json")
	if code != 0 {
		t.Fatalf("list failed: %d %s", code, stderr)
	}
	var documents []membox.DocumentView
	if err := json.Unmarshal([]byte(stdout), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("documents=%d", len(documents))
	}
	alpha, beta := documents[0], documents[1]
	if alpha.Title != "Alpha" {
		alpha, beta = beta, alpha
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "topic", "create", "Attention", "--json")
	if code != 0 {
		t.Fatalf("topic create failed: %d %s", code, stderr)
	}
	var created membox.CreateTopicResult
	if err := json.Unmarshal([]byte(stdout), &created); err != nil {
		t.Fatal(err)
	}
	if created.AlreadyExists || created.Topic.Name != "attention" {
		t.Fatalf("unexpected topic result: %+v", created)
	}
	code, stdout, stderr = runTestCLI(t, "--home", home, "topic", "create", "attention", "--json")
	if code != 0 {
		t.Fatalf("topic duplicate failed: %d %s", code, stderr)
	}
	var duplicate membox.CreateTopicResult
	if err := json.Unmarshal([]byte(stdout), &duplicate); err != nil {
		t.Fatal(err)
	}
	if !duplicate.AlreadyExists || duplicate.Topic.ID != created.Topic.ID {
		t.Fatalf("duplicate topic was not idempotent: %+v", duplicate)
	}

	if code, _, stderr := runTestCLI(t, "--home", home, "topic", "add", "attention", alpha.ID); code != 0 {
		t.Fatalf("assign topic failed: %d %s", code, stderr)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "link", "add", alpha.ID, beta.ID); code != 0 {
		t.Fatalf("link failed: %d %s", code, stderr)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "link", "add", alpha.ID, beta.ID); code != 0 {
		t.Fatalf("duplicate link failed: %d %s", code, stderr)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "link", "list", alpha.ID, "--json")
	if code != 0 {
		t.Fatalf("links failed: %d %s", code, stderr)
	}
	var graph membox.DocumentGraphView
	if err := json.Unmarshal([]byte(stdout), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Outgoing) != 1 || graph.Outgoing[0].ID != beta.ID || len(graph.Incoming) != 0 || len(graph.Topics) != 1 || graph.Topics[0].ID != created.Topic.ID {
		t.Fatalf("unexpected alpha graph: %+v", graph)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "link", "list", beta.ID, "--json")
	if code != 0 {
		t.Fatalf("reverse links failed: %d %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Incoming) != 1 || graph.Incoming[0].ID != alpha.ID || len(graph.Outgoing) != 0 || len(graph.Topics) != 0 {
		t.Fatalf("unexpected beta graph: %+v", graph)
	}

	code, stdout, stderr = runTestCLI(t, "--home", home, "topic", "documents", "attention", "--json")
	if code != 0 {
		t.Fatalf("topic documents failed: %d %s", code, stderr)
	}
	var topicDocuments membox.TopicDocumentsView
	if err := json.Unmarshal([]byte(stdout), &topicDocuments); err != nil {
		t.Fatal(err)
	}
	if len(topicDocuments.Documents) != 1 || topicDocuments.Documents[0].ID != alpha.ID {
		t.Fatalf("unexpected topic documents: %+v", topicDocuments)
	}

	if code, _, stderr := runTestCLI(t, "--home", home, "link", "remove", alpha.ID, beta.ID); code != 0 {
		t.Fatalf("unlink failed: %d %s", code, stderr)
	}
	if code, _, stderr := runTestCLI(t, "--home", home, "topic", "remove", "attention", alpha.ID); code != 0 {
		t.Fatalf("remove topic failed: %d %s", code, stderr)
	}
	code, stdout, stderr = runTestCLI(t, "--home", home, "link", "list", alpha.ID, "--json")
	if code != 0 {
		t.Fatalf("final links failed: %d %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Outgoing) != 0 || len(graph.Incoming) != 0 || len(graph.Topics) != 0 {
		t.Fatalf("graph was not cleared: %+v", graph)
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
