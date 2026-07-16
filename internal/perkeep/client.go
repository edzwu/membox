// Package perkeep wraps the official Perkeep Go client to provide a Go
// interface for uploading and downloading membox notes.
//
// It uses the HTTP JSON API via perkeep.org/pkg/client, rather than shelling
// out to pk-put/pk. This enables real CRUD operations, structured errors, and
// better performance.
package perkeep

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// Client provides access to a Perkeep server via the official Go client.
// The server URL and auth come from ~/.config/perkeep/client-config.json or
// are overridden explicitly.
type Client struct {
	// Server is the server URL override, if any. It is used for String.
	Server string
	c      *client.Client
}

// NewClient creates a Perkeep client using the default client config.
func NewClient() (*Client, error) {
	c, err := client.New()
	if err != nil {
		return nil, fmt.Errorf("new perkeep client: %w", err)
	}
	return &Client{c: c}, nil
}

// NewClientWithServer creates a Perkeep client pinned to a specific server URL.
// If server is just a host:port pair, it is normalized to http://host:port.
func NewClientWithServer(server string) (*Client, error) {
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	c, err := client.New(client.OptionServer(server))
	if err != nil {
		return nil, fmt.Errorf("new perkeep client for %s: %w", server, err)
	}
	// Setup auth from the client config (or env) for this server.
	if err := c.SetupAuth(); err != nil {
		return nil, fmt.Errorf("setup perkeep auth: %w", err)
	}
	return &Client{Server: server, c: c}, nil
}

// Close releases the underlying HTTP client resources.
func (c *Client) Close() error {
	if c.c != nil {
		return c.c.Close()
	}
	return nil
}

// UploadResult is returned by UploadNote.
type UploadResult struct {
	// Permanode is the blobref of the Perkeep permanode created for the note.
	Permanode string
	// FileRef is the blobref of the file (schema) blob that holds the content.
	FileRef string
}

// UploadNote uploads a markdown file to Perkeep and creates a permanode for it.
// The title is stored as the permanode title, tags are added as Perkeep tags,
// and the note UUID is recorded in a custom attribute so the note can be retrieved later.
func (c *Client) UploadNote(ctx context.Context, path string, title string, tags []string, uuid string) (*UploadResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("note file not found: %w", err)
	}
	defer f.Close()

	fileRef, err := c.c.UploadFile(ctx, filepath.Base(path), f, nil)
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}
	if !fileRef.Valid() {
		return nil, fmt.Errorf("upload file returned invalid blobref")
	}

	pr, err := c.c.UploadNewPermanode(ctx)
	if err != nil {
		return nil, fmt.Errorf("create permanode: %w", err)
	}
	permanode := pr.BlobRef
	if !permanode.Valid() {
		return nil, fmt.Errorf("create permanode returned invalid blobref")
	}

	attrs := []struct{ name, value string }{
		{"camliContent", fileRef.String()},
		{"title", title},
		{"membox-uuid", uuid},
	}
	for _, attr := range attrs {
		claim := schema.NewSetAttributeClaim(permanode, attr.name, attr.value)
		if _, err := c.c.UploadAndSignBlob(ctx, claim); err != nil {
			return nil, fmt.Errorf("set attribute %s: %w", attr.name, err)
		}
	}

	for _, tag := range tags {
		claim := schema.NewAddAttributeClaim(permanode, "tag", tag)
		if _, err := c.c.UploadAndSignBlob(ctx, claim); err != nil {
			return nil, fmt.Errorf("add tag %q: %w", tag, err)
		}
	}

	return &UploadResult{Permanode: permanode.String(), FileRef: fileRef.String()}, nil
}

// SearchByTag finds permanodes tagged with the given Perkeep tag.
func (c *Client) SearchByTag(ctx context.Context, tag string) ([]string, error) {
	q := &search.SearchQuery{
		Expression: "tag:" + tag,
		Limit:      -1,
	}
	res, err := c.c.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("search by tag: %w", err)
	}
	refs := make([]string, 0, len(res.Blobs))
	for _, b := range res.Blobs {
		refs = append(refs, b.Blob.String())
	}
	return refs, nil
}

// Describe returns the JSON description of a blobref.
func (c *Client) Describe(ctx context.Context, blobref string) ([]byte, error) {
	br, ok := blob.Parse(blobref)
	if !ok {
		return nil, fmt.Errorf("invalid blobref %q", blobref)
	}
	dr, err := c.c.Describe(ctx, &search.DescribeRequest{BlobRefs: []blob.Ref{br}})
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	return json.Marshal(dr)
}

// GetContents fetches the contents of a file or permanode blobref.
// If the blobref is a file/bytes schema, the file bytes are returned.
// If the blobref is a permanode, its camliContent is resolved and returned.
func (c *Client) GetContents(ctx context.Context, blobref string) ([]byte, error) {
	br, ok := blob.Parse(blobref)
	if !ok {
		return nil, fmt.Errorf("invalid blobref %q", blobref)
	}

	// Try reading as a file/bytes schema first.
	fr, err := schema.NewFileReader(ctx, c.c, br)
	if err == nil {
		defer fr.Close()
		return io.ReadAll(fr)
	}

	// Otherwise describe as a permanode and resolve camliContent.
	dr, err := c.c.Describe(ctx, &search.DescribeRequest{BlobRefs: []blob.Ref{br}})
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	meta, ok := dr.Meta[br.String()]
	if !ok || meta == nil || meta.Permanode == nil {
		return nil, fmt.Errorf("blobref %s is not a file or permanode", blobref)
	}
	contentRefStr := meta.Permanode.Attr.Get("camliContent")
	if contentRefStr == "" {
		return nil, fmt.Errorf("permanode %s has no camliContent", blobref)
	}
	contentRef, ok := blob.Parse(contentRefStr)
	if !ok {
		return nil, fmt.Errorf("invalid camliContent blobref %q", contentRefStr)
	}
	fr, err = schema.NewFileReader(ctx, c.c, contentRef)
	if err != nil {
		return nil, fmt.Errorf("open content file: %w", err)
	}
	defer fr.Close()
	return io.ReadAll(fr)
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
