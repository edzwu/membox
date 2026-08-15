package membox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/application"
	"membox/internal/domain/catalog"
	"membox/internal/pdfasset"
	"membox/internal/pdfconvert"
	"membox/internal/translation"
)

// ConvertPDFCommand runs the optional LAN PDF-to-Markdown feature. ServerURL
// overrides the persisted feature config for this call only.
type ConvertPDFCommand struct {
	Selector   string
	ServerURL  string
	OnProgress func(PDFConversionProgress)
}

// PDFConversionProgress is a truthful projection of one server NDJSON event.
// Page counters advance at chunk boundaries; zero values mean unavailable.
type PDFConversionProgress struct {
	Stage       string
	Description string
	PageFrom    int
	PageTo      int
	TotalPages  int
}

type ConvertedPDFChapterView struct {
	Title    string       `json:"title"`
	Filename string       `json:"filename"`
	Document DocumentView `json:"document"`
	Path     string       `json:"path"`
	Created  bool         `json:"created"`
}

type ConvertPDFResult struct {
	SourceDocumentID string                    `json:"source_document_id"`
	MarkdownDocument DocumentView              `json:"markdown_document"`
	MarkdownPath     string                    `json:"markdown_path"`
	MarkdownFilename string                    `json:"markdown_filename"`
	MarkdownSHA256   string                    `json:"markdown_sha256,omitempty"`
	Created          bool                      `json:"created"`
	Chapters         []ConvertedPDFChapterView `json:"chapters,omitempty"`
}

type PDFConverterConfigView struct {
	ServerURL  string `json:"server_url"`
	ConfigPath string `json:"config_path"`
}

func (b *Box) pdfConverterWorkflow() *pdfconvert.Workflow {
	workflow := pdfconvert.NewWorkflow(pdfconvert.NewHTTPClient(), pdfconvert.NewConfigStore(b.home))
	// Optional LLM structure planning, served by mmd → Pi → qwen3:14b. When
	// mmd is not running the planner call fails fast and postprocessing keeps
	// the converted document whole.
	client := translation.MMDClient{
		SocketPath: translation.SocketPath(b.home),
		Ensure: func(ctx context.Context) error {
			return translation.EnsureMMD(ctx, b.home)
		},
	}
	workflow.SetStructurePlanner(pdfconvert.LLMStructurePlanner{Complete: client.Complete})
	return workflow
}

// SetPDFConverterServer persists feature-owned configuration outside the core
// catalog database, under <membox-home>/pdf-converter.json.
func (b *Box) SetPDFConverterServer(_ context.Context, serverURL string) (PDFConverterConfigView, error) {
	workflow := b.pdfConverterWorkflow()
	serverURL, err := workflow.SetServerURL(serverURL)
	if err != nil {
		return PDFConverterConfigView{}, err
	}
	return PDFConverterConfigView{ServerURL: serverURL, ConfigPath: workflow.ConfigPath()}, nil
}

func (b *Box) GetPDFConverterConfig(_ context.Context) (PDFConverterConfigView, error) {
	workflow := b.pdfConverterWorkflow()
	config, err := workflow.Configuration()
	if err != nil {
		return PDFConverterConfigView{}, err
	}
	return PDFConverterConfigView{ServerURL: config.ServerURL, ConfigPath: workflow.ConfigPath()}, nil
}

func (b *Box) ConvertPDF(ctx context.Context, command ConvertPDFCommand) (ConvertPDFResult, error) {
	workspace := boxPDFConvertWorkspace{service: b.service}
	var onProgress func(pdfconvert.Progress)
	if command.OnProgress != nil {
		onProgress = func(progress pdfconvert.Progress) {
			command.OnProgress(PDFConversionProgress{
				Stage: progress.Stage, Description: progress.Description(),
				PageFrom: progress.PageFrom, PageTo: progress.PageTo, TotalPages: progress.TotalPages,
			})
		}
	}
	converted, err := b.pdfConverterWorkflow().ConvertWithProgress(ctx, workspace, command.Selector, command.ServerURL, onProgress)
	if err != nil {
		return ConvertPDFResult{}, err
	}
	document, path, err := b.service.ResolveDocument(ctx, converted.MarkdownDocumentID)
	if err != nil {
		return ConvertPDFResult{}, fmt.Errorf("resolving converted Markdown: %w", err)
	}
	chapters := make([]ConvertedPDFChapterView, 0, len(converted.Chapters))
	for _, chapter := range converted.Chapters {
		chapterDocument, chapterPath, err := b.service.ResolveDocument(ctx, chapter.DocumentID)
		if err != nil {
			return ConvertPDFResult{}, fmt.Errorf("resolving converted chapter %q: %w", chapter.Title, err)
		}
		chapters = append(chapters, ConvertedPDFChapterView{
			Title: chapter.Title, Filename: chapter.Filename, Document: documentView(chapterDocument, chapterPath),
			Path: chapter.Path, Created: chapter.Created,
		})
	}
	return ConvertPDFResult{
		SourceDocumentID: converted.SourceDocumentID,
		MarkdownDocument: documentView(document, path),
		MarkdownPath:     converted.MarkdownPath,
		MarkdownFilename: converted.MarkdownFilename,
		MarkdownSHA256:   converted.MarkdownSHA256,
		Created:          converted.Created,
		Chapters:         chapters,
	}, nil
}

type boxPDFConvertWorkspace struct {
	service *application.Service
}

func (w boxPDFConvertWorkspace) OpenPDF(ctx context.Context, selector string) (pdfconvert.Source, error) {
	document, path, err := w.service.ResolveDocument(ctx, selector)
	if err != nil {
		return pdfconvert.Source{}, err
	}
	if document.Status != catalog.DocumentActive {
		return pdfconvert.Source{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, path)
	}
	if document.Index.MediaType != "application/pdf" {
		return pdfconvert.Source{}, fmt.Errorf("document %s is %s, not a PDF", document.ID, document.Index.MediaType)
	}
	body, err := os.Open(path)
	if err != nil {
		return pdfconvert.Source{}, fmt.Errorf("opening PDF %q: %w", path, err)
	}
	return pdfconvert.Source{
		DocumentID: string(document.ID),
		Filename:   filepath.Base(path),
		MediaType:  document.Index.MediaType,
		Body:       body,
	}, nil
}

func (w boxPDFConvertWorkspace) ResolveBundleFilename(ctx context.Context, assetOwnerDocumentID, proposedFilename string) (string, error) {
	suffix, err := generatedPDFSuffix(assetOwnerDocumentID, proposedFilename)
	if err != nil {
		return "", err
	}
	document, existingPath, found, err := w.service.FindMarkdownByFilenameSuffix(ctx, suffix)
	if err != nil {
		return "", err
	}
	if !found {
		return proposedFilename, nil
	}
	if document.Status != catalog.DocumentActive {
		return "", fmt.Errorf("generated document %s is %s; restore it before publishing", document.ID, document.Status)
	}
	existingFilename := filepath.Base(existingPath)
	// An exact suffix is the old UUID-only convention. Return the readable
	// proposal so PublishBundle migrates it while preserving the UUID.
	if existingFilename == suffix {
		return proposedFilename, nil
	}
	// Once a readable prefix has been published, freeze it. Renaming the PDF
	// later must not churn generated paths or break chapter backlinks.
	return existingFilename, nil
}

func (w boxPDFConvertWorkspace) PublishBundle(ctx context.Context, assetOwnerDocumentID, filename, markdown string, assets []pdfconvert.Asset) (pdfconvert.PublishedMarkdown, error) {
	suffix, err := generatedPDFSuffix(assetOwnerDocumentID, filename)
	if err != nil {
		return pdfconvert.PublishedMarkdown{}, err
	}
	if existing, existingPath, found, findErr := w.service.FindMarkdownByFilenameSuffix(ctx, suffix); findErr != nil {
		return pdfconvert.PublishedMarkdown{}, findErr
	} else if found && filepath.Base(existingPath) != filename {
		if existing.Status != catalog.DocumentActive {
			return pdfconvert.PublishedMarkdown{}, fmt.Errorf("generated document %s is %s; restore it before publishing", existing.ID, existing.Status)
		}
		if _, renameErr := w.service.RenameDocument(ctx, string(existing.ID), filename, existing.Index.Title); renameErr != nil {
			return pdfconvert.PublishedMarkdown{}, fmt.Errorf("migrating generated Markdown filename: %w", renameErr)
		}
	}
	if len(assets) != 0 {
		owner, pdfPath, err := w.service.ResolveDocument(ctx, assetOwnerDocumentID)
		if err != nil {
			return pdfconvert.PublishedMarkdown{}, fmt.Errorf("resolving PDF asset owner: %w", err)
		}
		if owner.Status != catalog.DocumentActive || owner.Index.MediaType != "application/pdf" {
			return pdfconvert.PublishedMarkdown{}, fmt.Errorf("asset owner %s is not an active PDF", assetOwnerDocumentID)
		}
		assetRoot, err := pdfasset.Root(pdfPath, string(owner.ID))
		if err != nil {
			return pdfconvert.PublishedMarkdown{}, err
		}
		if err := publishConvertedAssets(ctx, assetRoot, assets); err != nil {
			return pdfconvert.PublishedMarkdown{}, err
		}
	}
	result, err := w.service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: markdown})
	if err != nil {
		return pdfconvert.PublishedMarkdown{}, err
	}
	return pdfconvert.PublishedMarkdown{DocumentID: string(result.Document.ID), Path: result.Path, Created: result.Created}, nil
}

func generatedPDFSuffix(assetOwnerDocumentID, filename string) (string, error) {
	identity := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(assetOwnerDocumentID), "-", ""))
	marker := "pdf-" + identity
	index := strings.LastIndex(strings.ToLower(filename), marker)
	if identity == "" || index < 0 {
		return "", fmt.Errorf("generated filename %q does not contain PDF identity", filename)
	}
	suffix := filename[index:]
	if filepath.Base(suffix) != suffix || strings.ToLower(filepath.Ext(suffix)) != ".md" {
		return "", fmt.Errorf("invalid generated PDF suffix %q", suffix)
	}
	return suffix, nil
}

func publishConvertedAssets(ctx context.Context, assetRoot string, assets []pdfconvert.Asset) error {
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := pdfasset.ImageTarget(assetRoot, asset.RelativePath)
		if err != nil {
			return err
		}
		if existing, err := os.ReadFile(target); err == nil {
			if !bytes.Equal(existing, asset.Body) {
				return fmt.Errorf("converted asset collision at %s", target)
			}
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("reading converted asset %q: %w", target, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating converted image directory: %w", err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".membox-image-*")
		if err != nil {
			return fmt.Errorf("creating converted asset: %w", err)
		}
		tmpPath := tmp.Name()
		if chmodErr := tmp.Chmod(0o600); chmodErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return chmodErr
		}
		_, writeErr := tmp.Write(asset.Body)
		closeErr := tmp.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("writing converted asset %q: %w", target, errors.Join(writeErr, closeErr))
		}
		if err := os.Rename(tmpPath, target); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("publishing converted asset %q: %w", target, err)
		}
	}
	return nil
}

func (w boxPDFConvertWorkspace) LinkDocuments(ctx context.Context, fromDocumentID, toDocumentID string) error {
	_, err := w.service.LinkDocuments(ctx, fromDocumentID, toDocumentID)
	return err
}
