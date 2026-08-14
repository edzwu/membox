package pdfconvert

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const pdfMediaType = "application/pdf"

type Workflow struct {
	client Client
	config ConfigStore
}

func NewWorkflow(client Client, config ConfigStore) *Workflow {
	return &Workflow{client: client, config: config}
}

func (w *Workflow) ConfigPath() string { return w.config.Path() }

func (w *Workflow) Configuration() (Config, error) { return w.config.Load() }

func (w *Workflow) ServerURL() (string, error) {
	config, err := w.config.Load()
	if err != nil {
		return "", err
	}
	return config.ServerURL, nil
}

func (w *Workflow) SetServerURL(serverURL string) (string, error) {
	config, err := w.config.SaveServerURL(serverURL)
	return config.ServerURL, err
}

func (w *Workflow) Convert(ctx context.Context, workspace Workspace, selector, serverOverride string) (Result, error) {
	if w == nil || w.client == nil {
		return Result{}, errors.New("PDF converter client is unavailable")
	}
	if workspace == nil {
		return Result{}, errors.New("PDF converter workspace is unavailable")
	}
	config, err := w.config.Load()
	if err != nil {
		return Result{}, err
	}
	serverURL := strings.TrimRight(strings.TrimSpace(serverOverride), "/")
	if serverURL == "" {
		serverURL = config.ServerURL
	}
	if serverURL == "" {
		return Result{}, fmt.Errorf("PDF converter server is not configured; run :pdf server <url> or set %s", ServerURLEnv)
	}
	if err := ValidateServerURL(serverURL); err != nil {
		return Result{}, err
	}

	source, err := workspace.OpenPDF(ctx, strings.TrimSpace(selector))
	if err != nil {
		return Result{}, err
	}
	if source.Body == nil {
		return Result{}, errors.New("PDF source body is unavailable")
	}
	defer source.Body.Close()
	if source.MediaType != pdfMediaType && !strings.EqualFold(filepath.Ext(source.Filename), ".pdf") {
		return Result{}, fmt.Errorf("document %s is %s, not a PDF", source.DocumentID, source.MediaType)
	}

	remote, err := w.client.Convert(ctx, serverURL, source.Filename, source.Body)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(remote.Markdown) == "" {
		return Result{}, errors.New("converter returned empty Markdown")
	}
	filename := convertedFilename(source)
	processed := PostprocessMarkdown(remote.Markdown, filename)
	if len(processed.Chapters) == 0 {
		published, err := workspace.PublishBundle(ctx, filename, processed.IndexMarkdown, remote.Assets)
		if err != nil {
			return Result{}, fmt.Errorf("publishing converted PDF bundle: %w", err)
		}
		if err := workspace.LinkDocuments(ctx, source.DocumentID, published.DocumentID); err != nil {
			return Result{}, fmt.Errorf("linking PDF to converted Markdown: %w", err)
		}
		return conversionResult(source, filename, remote.MarkdownSHA256, published, nil), nil
	}

	publishedChapters := make([]PublishedChapter, 0, len(processed.Chapters))
	for index, chapter := range processed.Chapters {
		assets := []Asset(nil)
		if index == 0 {
			assets = remote.Assets
		}
		published, err := workspace.PublishBundle(ctx, chapter.Filename, chapter.Markdown, assets)
		if err != nil {
			return Result{}, fmt.Errorf("publishing converted PDF chapter %q: %w", chapter.Title, err)
		}
		publishedChapters = append(publishedChapters, PublishedChapter{
			Title: chapter.Title, Filename: chapter.Filename, DocumentID: published.DocumentID,
			Path: published.Path, Created: published.Created,
		})
	}
	indexDocument, err := workspace.PublishBundle(ctx, filename, processed.IndexMarkdown, nil)
	if err != nil {
		return Result{}, fmt.Errorf("publishing converted PDF index: %w", err)
	}
	if err := workspace.LinkDocuments(ctx, source.DocumentID, indexDocument.DocumentID); err != nil {
		return Result{}, fmt.Errorf("linking PDF to converted Markdown index: %w", err)
	}
	for _, chapter := range publishedChapters {
		if err := workspace.LinkDocuments(ctx, indexDocument.DocumentID, chapter.DocumentID); err != nil {
			return Result{}, fmt.Errorf("linking converted Markdown index to chapter %q: %w", chapter.Title, err)
		}
		if err := workspace.LinkDocuments(ctx, chapter.DocumentID, indexDocument.DocumentID); err != nil {
			return Result{}, fmt.Errorf("linking converted Markdown chapter %q to index: %w", chapter.Title, err)
		}
	}
	return conversionResult(source, filename, remote.MarkdownSHA256, indexDocument, publishedChapters), nil
}

func conversionResult(source Source, filename, markdownSHA256 string, published PublishedMarkdown, chapters []PublishedChapter) Result {
	return Result{
		SourceDocumentID: source.DocumentID, MarkdownDocumentID: published.DocumentID,
		MarkdownPath: published.Path, MarkdownFilename: filename, MarkdownSHA256: markdownSHA256,
		Created: published.Created, Chapters: chapters,
	}
}

func convertedFilename(source Source) string {
	// Only stable catalog identity participates in the generated path. PDF
	// titles and filesystem names are editable, but reconversion must update
	// the same Markdown document and preserve its UUID.
	identity := strings.ToLower(strings.ReplaceAll(source.DocumentID, "-", ""))
	if identity == "" {
		identity = "document"
	}
	return "pdf-" + identity + ".md"
}
