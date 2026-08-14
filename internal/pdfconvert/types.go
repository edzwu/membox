package pdfconvert

import (
	"context"
	"fmt"
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
	ResolveBundleFilename(ctx context.Context, assetOwnerDocumentID, proposedFilename string) (string, error)
	PublishBundle(ctx context.Context, assetOwnerDocumentID, filename, markdown string, assets []Asset) (PublishedMarkdown, error)
	LinkDocuments(ctx context.Context, fromDocumentID, toDocumentID string) error
}

type RemoteResult struct {
	Filename       string
	Markdown       string
	MarkdownSHA256 string
	Assets         []Asset
}

// Progress is one server-emitted NDJSON progress event. Page counters describe
// completed/current chunks rather than a fabricated continuously increasing
// percentage.
type Progress struct {
	Stage             string  `json:"stage"`
	Detail            string  `json:"detail,omitempty"`
	Index             int     `json:"index,omitempty"`
	PageFrom          int     `json:"page_from,omitempty"`
	PageTo            int     `json:"page_to,omitempty"`
	TotalPages        int     `json:"total_pages,omitempty"`
	InitialChunkPages int     `json:"initial_chunk_pages,omitempty"`
	NextChunkPages    int     `json:"next_chunk_pages,omitempty"`
	Chunks            int     `json:"chunks,omitempty"`
	MemTargetPct      float64 `json:"mem_target_pct,omitempty"`
	PeakDeltaMB       float64 `json:"peak_delta_mb,omitempty"`
}

func (p Progress) Description() string {
	switch p.Stage {
	case "split":
		return fmt.Sprintf("%d pages · initial chunks of %d", p.TotalPages, p.InitialChunkPages)
	case "chunk_start":
		return fmt.Sprintf("pages %d–%d/%d converting", p.PageFrom, p.PageTo, p.TotalPages)
	case "chunk_done":
		return fmt.Sprintf("pages %d–%d/%d done · next %d", p.PageFrom, p.PageTo, p.TotalPages, p.NextChunkPages)
	case "merge":
		return fmt.Sprintf("merging %d chunks", p.Chunks)
	case "publish":
		return "publishing Markdown"
	default:
		if p.Detail != "" {
			return p.Detail
		}
		return p.Stage
	}
}

// Client knows the remote converter protocol but nothing about membox.
type Client interface {
	Convert(ctx context.Context, serverURL, filename string, body io.Reader, onProgress func(Progress)) (RemoteResult, error)
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
