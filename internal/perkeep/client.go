// Package perkeep wraps the Perkeep command-line tools (pk, pk-put, pk-get) to
// provide a Go interface for uploading and downloading membox notes.
//
// It intentionally shells out to the existing Perkeep binaries rather than
// importing the Perkeep Go client, because the local ~/.config/perkeep/client-config.json
// already carries all the authentication and server identity configuration.
package perkeep

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client provides access to a Perkeep server via the local pk/pk-put/pk-get
// binaries. The server URL and auth come from ~/.config/perkeep/client-config.json.
type Client struct {
	// Server overrides the server prefix. If empty, pk uses the default from
	// the client configuration file.
	Server string
}

// NewClient creates a Perkeep client that uses the default client config.
func NewClient() *Client {
	return &Client{}
}

// NewClientWithServer creates a Perkeep client pinned to a specific server URL.
func NewClientWithServer(server string) *Client {
	return &Client{Server: server}
}

// serverFlag returns the -server flag argument if a server override is set.
func (c *Client) serverFlag() []string {
	if c.Server == "" {
		return nil
	}
	return []string{"-server=" + c.Server}
}

// UploadResult is returned by UploadNote.
type UploadResult struct {
	// Permanode is the blobref of the Perkeep permanode created for the note.
	Permanode string
	// FileRef is the blobref of the file (schema) blob that holds the content.
	FileRef string
}

// UploadNote uploads a markdown file to Perkeep and creates a permanode for it.
// The title is stored as the permanode title, tags are added as Perkeep tags, and
// the note UUID is recorded in a custom attribute so the note can be retrieved later.
func (c *Client) UploadNote(ctx context.Context, path string, title string, tags []string, uuid string) (*UploadResult, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("note file not found: %w", err)
	}

	args := []string{"file"}
	args = append(args, c.serverFlag()...)
	args = append(args, "--permanode", "--title="+title)

	// Tag with membox plus the note UUID, then any user tags.
	allTags := append([]string{"membox", "uuid:" + uuid}, tags...)
	args = append(args, "--tag="+strings.Join(allTags, ","))
	args = append(args, path)

	cmd := exec.CommandContext(ctx, "pk-put", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pk-put failed: %s: %w", string(ee.Stderr), err)
		}
		return nil, fmt.Errorf("pk-put failed: %w", err)
	}

	permanode := strings.TrimSpace(string(out))
	if permanode == "" {
		return nil, fmt.Errorf("pk-put returned empty permanode")
	}

	// Record the membox UUID as a custom attribute on the permanode.
	if err := c.setAttr(ctx, permanode, "membox-uuid", uuid); err != nil {
		return nil, fmt.Errorf("set membox-uuid attribute: %w", err)
	}

	return &UploadResult{Permanode: permanode}, nil
}

func (c *Client) setAttr(ctx context.Context, permanode, name, value string) error {
	args := append([]string{"attr"}, c.serverFlag()...)
	args = append(args, permanode, name, value)
	cmd := exec.CommandContext(ctx, "pk-put", args...)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return fmt.Errorf("pk-put attr failed: %s: %w", string(ee.Stderr), err)
		}
		return fmt.Errorf("pk-put attr failed: %w", err)
	}
	return nil
}

// SearchByTag finds permanodes tagged with the given Perkeep tag.
func (c *Client) SearchByTag(ctx context.Context, tag string) ([]string, error) {
	args := []string{"search"}
	args = append(args, c.serverFlag()...)
	args = append(args, "-1", "tag:"+tag)
	cmd := exec.CommandContext(ctx, "pk", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pk search failed: %s: %w", string(ee.Stderr), err)
		}
		return nil, fmt.Errorf("pk search failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var refs []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// Describe returns the JSON description of a blobref.
func (c *Client) Describe(ctx context.Context, blobref string) ([]byte, error) {
	args := []string{"describe"}
	args = append(args, c.serverFlag()...)
	args = append(args, blobref)
	cmd := exec.CommandContext(ctx, "pk", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pk describe failed: %s: %w", string(ee.Stderr), err)
		}
		return nil, fmt.Errorf("pk describe failed: %w", err)
	}
	return out, nil
}

// GetContents fetches the contents of a file or bytes blobref.
func (c *Client) GetContents(ctx context.Context, blobref string) ([]byte, error) {
	args := []string{"get"}
	args = append(args, c.serverFlag()...)
	args = append(args, "-contents", blobref)
	cmd := exec.CommandContext(ctx, "pk", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pk get failed: %s: %w", string(ee.Stderr), err)
		}
		return nil, fmt.Errorf("pk get failed: %w", err)
	}
	return out, nil
}

// HashContent computes the SHA-256 content hash used to detect changes.
func HashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// WriteTempNote writes note content to a temporary file in the given dir and
// returns its path. The caller is responsible for removing the file.
func WriteTempNote(dir string, content []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "perkeep-note-*.md")
	if err != nil {
		return "", fmt.Errorf("create temp note: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return "", fmt.Errorf("write temp note: %w", err)
	}
	return f.Name(), nil
}

// ComposeMarkdown renders a note as the markdown that should be stored in
// Perkeep, including the UUID frontmatter.
func ComposeMarkdown(uuid, content string) []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	b.WriteString("uuid: " + uuid + "\n")
	b.WriteString("---\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// CacheDir returns the cache directory for a note under the provided root.
func CacheDir(root, uuid string) string {
	return filepath.Join(root, uuid[:2], uuid)
}
