package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

const SettingPDFPath = "pdf_path"

type ImportPDFOptions struct {
	SourcePath      string
	DestinationRoot string
	Title           string
	Authors         string
	Year            int
	Keywords        string
}

type ImportPDFResult struct {
	Document *catalog.Document
	Path     string
}

// ImportPDF copies one PDF into the configured PDF root, then catalogs its
// filesystem bytes and extracted metadata/text. SQLite never owns the binary.
func (s *Service) ImportPDF(ctx context.Context, opts ImportPDFOptions) (ImportPDFResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return ImportPDFResult{}, lockErr
	}
	defer release()

	source := strings.TrimSpace(opts.SourcePath)
	if source == "" {
		return ImportPDFResult{}, errors.New("PDF source path is required")
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return ImportPDFResult{}, fmt.Errorf("resolving PDF source: %w", err)
	}
	absoluteSource, err = filepath.EvalSymlinks(filepath.Clean(absoluteSource))
	if err != nil {
		return ImportPDFResult{}, fmt.Errorf("resolving PDF source %q: %w", source, err)
	}
	info, err := os.Stat(absoluteSource)
	if err != nil {
		return ImportPDFResult{}, fmt.Errorf("reading PDF source %q: %w", absoluteSource, err)
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(absoluteSource), ".pdf") {
		return ImportPDFResult{}, fmt.Errorf("source must be a regular .pdf file: %s", absoluteSource)
	}

	root := strings.TrimSpace(opts.DestinationRoot)
	if root == "" {
		root, err = s.store.GetSetting(ctx, SettingPDFPath)
		if err != nil {
			return ImportPDFResult{}, err
		}
	}
	if strings.TrimSpace(root) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return ImportPDFResult{}, fmt.Errorf("resolving default PDF directory: %w", homeErr)
		}
		root = filepath.Join(home, "Documents", "membox-pdfs")
	}
	expandedRoot, err := expandUserHome(root)
	if err != nil {
		return ImportPDFResult{}, err
	}
	if err := os.MkdirAll(expandedRoot, 0o755); err != nil {
		return ImportPDFResult{}, fmt.Errorf("creating PDF directory %q: %w", expandedRoot, err)
	}
	canonicalRoot, err := s.scanner.Canonicalize(expandedRoot)
	if err != nil {
		return ImportPDFResult{}, err
	}
	indexedPath, _, err := s.store.AddOrReactivatePath(ctx, canonicalRoot, s.clock.Now())
	if err != nil {
		return ImportPDFResult{}, err
	}
	if err := s.store.SetSetting(ctx, SettingPDFPath, canonicalRoot); err != nil {
		return ImportPDFResult{}, err
	}

	filename := filepath.Base(absoluteSource)
	target := uniquePDFPath(canonicalRoot, filename)
	body, err := s.reader.Read(ctx, absoluteSource)
	if err != nil {
		return ImportPDFResult{}, err
	}
	if len(body) < 5 || string(body[:5]) != "%PDF-" {
		return ImportPDFResult{}, fmt.Errorf("%s does not have a PDF header", absoluteSource)
	}
	if err := s.writer.WriteNew(ctx, target, body); err != nil {
		return ImportPDFResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.writer.Remove(context.Background(), target)
		}
	}()

	relative := filepath.ToSlash(filepath.Base(target))
	location, err := catalog.NewLocation(indexedPath.ID, relative)
	if err != nil {
		return ImportPDFResult{}, err
	}
	observation, err := s.scanner.ObserveFile(ctx, location, target)
	if err != nil {
		return ImportPDFResult{}, err
	}
	id, err := s.ids.NewDocumentID()
	if err != nil {
		return ImportPDFResult{}, err
	}
	document, err := catalog.NewDocument(id, observation, s.clock.Now())
	if err != nil {
		return ImportPDFResult{}, err
	}
	if title := strings.TrimSpace(opts.Title); title != "" {
		document.Index.Title = title
		document.Index.MetadataOverrides |= catalog.MetadataTitleOverride
	}
	if authors := strings.TrimSpace(opts.Authors); authors != "" {
		document.Index.Authors = authors
		document.Index.MetadataOverrides |= catalog.MetadataAuthorsOverride
	}
	if opts.Year != 0 {
		if err := validatePublicationYear(opts.Year); err != nil {
			return ImportPDFResult{}, err
		}
		document.Index.Year = opts.Year
		document.Index.MetadataOverrides |= catalog.MetadataYearOverride
	}
	if keywords := strings.TrimSpace(opts.Keywords); keywords != "" {
		document.Index.Keywords = keywords
		document.Index.MetadataOverrides |= catalog.MetadataKeywordsOverride
	}
	if err := s.saveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true}); err != nil {
		return ImportPDFResult{}, err
	}
	committed = true
	return ImportPDFResult{Document: document, Path: target}, nil
}

func expandUserHome(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	if value == "~" {
		return home, nil
	}
	return filepath.Join(home, value[2:]), nil
}

func uniquePDFPath(root, filename string) string {
	candidate := filepath.Join(root, filename)
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	ext := filepath.Ext(filename)
	for index := 2; fileExists(candidate); index++ {
		candidate = filepath.Join(root, fmt.Sprintf("%s-%d%s", stem, index, ext))
	}
	return candidate
}

type DocumentMetadataPatch struct {
	Title    *string
	Authors  *string
	Year     *int
	Keywords *string
}

// UpdateDocumentMetadata updates searchable catalog metadata without rewriting
// the PDF binary. A later scan preserves these fields.
func (s *Service) UpdateDocumentMetadata(ctx context.Context, selector string, patch DocumentMetadataPatch) (*catalog.Document, string, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return nil, "", lockErr
	}
	defer release()
	document, absolute, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return nil, "", err
	}
	if document.Status != catalog.DocumentActive {
		return nil, "", fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	if document.Index.MediaType != "application/pdf" {
		return nil, "", fmt.Errorf("document %s is %s, not a PDF", document.ID, document.Index.MediaType)
	}
	if patch.Title == nil && patch.Authors == nil && patch.Year == nil && patch.Keywords == nil {
		return nil, "", errors.New("at least one metadata field is required")
	}
	observation, err := s.scanner.ObserveFile(ctx, document.Location, absolute)
	if err != nil {
		return nil, "", err
	}
	if err := document.Observe(observation, s.clock.Now()); err != nil {
		return nil, "", err
	}
	if patch.Title != nil {
		document.Index.Title = strings.TrimSpace(*patch.Title)
		if document.Index.Title == "" {
			document.Index.Title = strings.TrimSuffix(filepath.Base(absolute), filepath.Ext(absolute))
		}
		document.Index.MetadataOverrides |= catalog.MetadataTitleOverride
	}
	if patch.Authors != nil {
		document.Index.Authors = strings.TrimSpace(*patch.Authors)
		document.Index.MetadataOverrides |= catalog.MetadataAuthorsOverride
	}
	if patch.Year != nil {
		if err := validatePublicationYear(*patch.Year); err != nil {
			return nil, "", err
		}
		document.Index.Year = *patch.Year
		document.Index.MetadataOverrides |= catalog.MetadataYearOverride
	}
	if patch.Keywords != nil {
		document.Index.Keywords = strings.TrimSpace(*patch.Keywords)
		document.Index.MetadataOverrides |= catalog.MetadataKeywordsOverride
	}
	if err := s.saveDocument(ctx, port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true}); err != nil {
		return nil, "", err
	}
	return document, absolute, nil
}

func validatePublicationYear(year int) error {
	if year != 0 && (year < 1000 || year > 9999) {
		return fmt.Errorf("publication year must be 0 or a four-digit year")
	}
	return nil
}
