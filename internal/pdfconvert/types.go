package pdfconvert

import (
	"context"
	"io"
)

// Source is the narrow projection of a membox PDF needed by this feature.
// The feature never depends on catalog or application-layer types.
type Source struct {
	DocumentID string
	Filename   string
	MediaType  string
	Body       io.ReadCloser
}

// PublishedMarkdown is returned by the host after it atomically publishes and
// indexes the converter result in its normal Markdown authority.
type PublishedMarkdown struct {
	DocumentID string
	Path       string
	Created    bool
}

// Asset is one safe, relative file from the converter bundle. Current server
// output uses content-addressed images/<sha256>.<ext> names.
type Asset struct {
	RelativePath string
	Body         []byte
}

// Workspace is the only boundary from the optional converter feature back to
// membox core. Implementations adapt existing resolve/upsert/link operations.
type Workspace interface {
	OpenPDF(ctx context.Context, selector string) (Source, error)
	PublishBundle(ctx context.Context, assetOwnerDocumentID, filename, markdown string, assets []Asset) (PublishedMarkdown, error)
	LinkDocuments(ctx context.Context, fromDocumentID, toDocumentID string) error
}

type RemoteResult struct {
	Filename       string
	Markdown       string
	MarkdownSHA256 string
	Assets         []Asset
}

// Client knows the remote converter protocol but nothing about membox.
type Client interface {
	Convert(ctx context.Context, serverURL, filename string, body io.Reader) (RemoteResult, error)
}

type PublishedChapter struct {
	Title      string
	Filename   string
	DocumentID string
	Path       string
	Created    bool
}

type Result struct {
	SourceDocumentID   string
	MarkdownDocumentID string
	MarkdownPath       string
	MarkdownFilename   string
	MarkdownSHA256     string
	Created            bool
	Chapters           []PublishedChapter
}
