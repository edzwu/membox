package pdfconvert

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const pdfMediaType = "application/pdf"

// minPlannerMarkdownBytes gates the LLM structure planner: below this size a
// converted document is already readable whole.
const minPlannerMarkdownBytes = 40 << 10

type Workflow struct {
	client  Client
	config  ConfigStore
	planner StructurePlanner
}

func NewWorkflow(client Client, config ConfigStore) *Workflow {
	return &Workflow{client: client, config: config}
}

// SetStructurePlanner installs the optional LLM fallback used when
// deterministic chapter splitting finds fewer than two chapters.
func (w *Workflow) SetStructurePlanner(planner StructurePlanner) { w.planner = planner }

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
	return w.ConvertWithProgress(ctx, workspace, selector, serverOverride, nil)
}

func (w *Workflow) ConvertWithProgress(ctx context.Context, workspace Workspace, selector, serverOverride string, onProgress func(Progress)) (Result, error) {
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

	remote, err := w.client.Convert(ctx, serverURL, source.Filename, source.Body, onProgress)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(remote.Markdown) == "" {
		return Result{}, errors.New("converter returned empty Markdown")
	}
	if onProgress != nil {
		onProgress(Progress{Stage: "publish"})
	}
	filename, err := workspace.ResolveBundleFilename(ctx, source.DocumentID, convertedFilename(source))
	if err != nil {
		return Result{}, fmt.Errorf("resolving stable converted filename: %w", err)
	}
	remote.Markdown = rewriteAssetReferences(remote.Markdown, source.DocumentID, remote.Assets)
	planner := w.planner
	// LLM planning is only worth a local-model turn for documents large
	// enough to benefit from splitting; small PDFs stay on the deterministic
	// path (or whole) without spending a minute of model time.
	if planner != nil && len(remote.Markdown) < minPlannerMarkdownBytes {
		planner = nil
	}
	if planner != nil && onProgress != nil {
		planner = progressStructurePlanner{inner: planner, onProgress: onProgress}
	}
	processed := PostprocessMarkdownWithPlanner(ctx, remote.Markdown, filename, planner)
	if len(processed.Chapters) == 0 {
		published, err := workspace.PublishBundle(ctx, source.DocumentID, filename, processed.IndexMarkdown, remote.Assets)
		if err != nil {
			return Result{}, fmt.Errorf("publishing converted PDF bundle: %w", err)
		}
		if err := workspace.LinkDocuments(ctx, source.DocumentID, published.DocumentID); err != nil {
			return Result{}, fmt.Errorf("linking PDF to converted Markdown: %w", err)
		}
		return conversionResult(source, filename, remote.MarkdownSHA256, published, nil), nil
	}

	publishedChapters := make([]PublishedChapter, 0, len(processed.Chapters))
	filenameToID := make(map[string]string, len(processed.Chapters)+1)
	for index, chapter := range processed.Chapters {
		assets := []Asset(nil)
		if index == 0 {
			assets = remote.Assets
		}
		published, err := workspace.PublishBundle(ctx, source.DocumentID, chapter.Filename, chapter.Markdown, assets)
		if err != nil {
			return Result{}, fmt.Errorf("publishing converted PDF chapter %q: %w", chapter.Title, err)
		}
		publishedChapters = append(publishedChapters, PublishedChapter{
			Title: chapter.Title, Filename: chapter.Filename, DocumentID: published.DocumentID,
			Path: published.Path, Created: published.Created,
		})
		filenameToID[strings.ToLower(filepath.Base(chapter.Filename))] = published.DocumentID
	}
	// Bake catalog UUIDs into the TOC before publish. Filename .md hrefs are
	// fragile in the browser (markdown-it percent-encodes CJK paths); /?id=
	// is the same identity the conversion graph already stores.
	indexMarkdown := rewriteRelativeMarkdownLinks(processed.IndexMarkdown, filenameToID)
	indexDocument, err := workspace.PublishBundle(ctx, source.DocumentID, filename, indexMarkdown, nil)
	if err != nil {
		return Result{}, fmt.Errorf("publishing converted PDF index: %w", err)
	}
	// Chapter → index backlinks still use the stable index filename; Miru
	// rewrites them via /series (filename→id). TOC is identity-linked above.
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

func rewriteAssetReferences(markdown, sourceDocumentID string, assets []Asset) string {
	prefix := "/api/pdf-assets/" + url.PathEscape(sourceDocumentID) + "/"
	for _, asset := range assets {
		// Markdown link destinations cannot contain raw spaces, parentheses, or
		// other URL-significant characters; percent-encode each path segment.
		segments := strings.Split(asset.RelativePath, "/")
		for index, segment := range segments {
			segments[index] = url.PathEscape(segment)
		}
		markdown = strings.ReplaceAll(markdown, asset.RelativePath, prefix+strings.Join(segments, "/"))
	}
	return markdown
}

// progressStructurePlanner surfaces the LLM analysis step in CLI/TUI
// progress without changing planner semantics.
type progressStructurePlanner struct {
	inner      StructurePlanner
	onProgress func(Progress)
}

func (p progressStructurePlanner) PlanChapters(ctx context.Context, request PlanRequest) ([]ChapterPlan, error) {
	p.onProgress(Progress{Stage: "structure", Detail: "analyzing document structure with local model"})
	return p.inner.PlanChapters(ctx, request)
}

func conversionResult(source Source, filename, markdownSHA256 string, published PublishedMarkdown, chapters []PublishedChapter) Result {
	return Result{
		SourceDocumentID: source.DocumentID, MarkdownDocumentID: published.DocumentID,
		MarkdownPath: published.Path, MarkdownFilename: filename, MarkdownSHA256: markdownSHA256,
		Created: published.Created, Chapters: chapters,
	}
}

func convertedFilename(source Source) string {
	// The readable prefix is frozen by Workspace.ResolveBundleFilename after
	// first publication. The full source UUID remains in the suffix so legacy
	// files can be migrated and reconversion can always recover stable identity.
	// The marker is a single dash; the suffix is what generatedPDFSuffix and
	// the TUI's converted mark both rely on.
	identity := strings.ToLower(strings.ReplaceAll(source.DocumentID, "-", ""))
	if identity == "" {
		identity = "document"
	}
	return readablePDFStem(source.Filename) + "-pdf-" + identity + ".md"
}

func readablePDFStem(filename string) string {
	const maxBytes = 120
	stem := strings.TrimSpace(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	var output strings.Builder
	separator := false
	for _, r := range stem {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if separator && output.Len() > 0 && output.Len()+1 <= maxBytes {
				output.WriteByte('-')
			}
			separator = false
			if output.Len()+utf8.RuneLen(r) > maxBytes {
				continue
			}
			output.WriteRune(r)
		case r == '-' || r == '_' || unicode.IsSpace(r):
			separator = output.Len() > 0
		default:
			separator = output.Len() > 0
		}
	}
	value := strings.Trim(output.String(), "-")
	if value == "" {
		return "document"
	}
	return value
}
