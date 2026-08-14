package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	pdfreader "github.com/ledongthuc/pdf"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

type Scanner struct{}

func NewScanner() *Scanner { return &Scanner{} }

func (s *Scanner) Canonicalize(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("directory is required")
	}
	expanded, err := expandHome(directory)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", directory, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("reading path %q: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", canonical)
	}
	file, err := os.Open(canonical)
	if err != nil {
		return "", fmt.Errorf("opening directory %q: %w", canonical, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing directory %q: %w", canonical, err)
	}
	return canonical, nil
}

func expandHome(value string) (string, error) {
	if value != "~" && !strings.HasPrefix(value, "~/") && !strings.HasPrefix(value, `~\`) {
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

func (s *Scanner) Scan(ctx context.Context, indexedPath catalog.IndexedPath) (port.ScanResult, error) {
	var result port.ScanResult
	err := filepath.WalkDir(indexedPath.Root, func(fullPath string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			result.Issues = append(result.Issues, port.ScanIssue{Path: fullPath, Err: walkErr})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == catalog.TrashDir {
				// The trash directory holds soft-deleted documents; it is never
				// indexed, so trashed documents stay hidden until purged.
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !isSupportedDocument(fullPath) {
			return nil
		}
		relative, err := filepath.Rel(indexedPath.Root, fullPath)
		if err != nil {
			result.Issues = append(result.Issues, port.ScanIssue{Path: fullPath, Err: err})
			return nil
		}
		location, err := catalog.NewLocation(indexedPath.ID, filepath.ToSlash(relative))
		if err != nil {
			result.Issues = append(result.Issues, port.ScanIssue{Path: fullPath, Err: err})
			return nil
		}
		observation, err := s.ObserveFile(ctx, location, fullPath)
		if err != nil {
			result.Issues = append(result.Issues, port.ScanIssue{Path: fullPath, Err: err})
			return nil
		}
		result.Observations = append(result.Observations, observation)
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("scanning %q: %w", indexedPath.Root, err)
	}
	return result, nil
}

func (s *Scanner) ObserveFile(ctx context.Context, location catalog.Location, absolutePath string) (catalog.Observation, error) {
	if err := ctx.Err(); err != nil {
		return catalog.Observation{}, err
	}
	body, err := os.ReadFile(absolutePath)
	if err != nil {
		return catalog.Observation{}, fmt.Errorf("reading document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return catalog.Observation{}, err
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return catalog.Observation{}, fmt.Errorf("stating document: %w", err)
	}
	digest := sha256.Sum256(body)
	observation := catalog.Observation{
		Location: location, FileKey: fileKey(info), MTime: info.ModTime().UnixNano(), Size: info.Size(),
		SHA256: hex.EncodeToString(digest[:]), Body: body,
	}
	switch strings.ToLower(filepath.Ext(absolutePath)) {
	case ".md", ".markdown":
		if !utf8.Valid(body) {
			return catalog.Observation{}, errors.New("Markdown is not valid UTF-8")
		}
		observation.MediaType = "text/markdown"
		observation.Title = extractTitle(body, absolutePath)
		observation.SearchText = body
	case ".pdf":
		if len(body) < 5 || string(body[:5]) != "%PDF-" {
			return catalog.Observation{}, errors.New("file does not have a PDF header")
		}
		metadata, extractErr := extractPDF(absolutePath)
		if extractErr != nil {
			return catalog.Observation{}, extractErr
		}
		observation.MediaType = "application/pdf"
		observation.Title = metadata.Title
		observation.Authors = metadata.Authors
		observation.Year = metadata.Year
		observation.Keywords = metadata.Keywords
		observation.PageCount = metadata.PageCount
		observation.SearchText = []byte(metadata.Text)
	default:
		return catalog.Observation{}, fmt.Errorf("unsupported document type %q", filepath.Ext(absolutePath))
	}
	return observation, nil
}

type Writer struct{}

func (Writer) WriteNew(ctx context.Context, absolutePath string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(absolutePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating document %q: %w", absolutePath, err)
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return fmt.Errorf("writing document %q: %w", absolutePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing document %q: %w", absolutePath, err)
	}
	return nil
}

func (Writer) Write(ctx context.Context, absolutePath string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(absolutePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("opening document %q for writing: %w", absolutePath, err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return fmt.Errorf("writing document %q: %w", absolutePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing document %q: %w", absolutePath, err)
	}
	return ctx.Err()
}

func (Writer) Remove(ctx context.Context, absolutePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(absolutePath); err != nil {
		return fmt.Errorf("deleting document %q: %w", absolutePath, err)
	}
	return nil
}

func (Writer) Move(ctx context.Context, fromPath, toPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return fmt.Errorf("renaming document %q: %w", fromPath, err)
	}
	return nil
}

func isMarkdown(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func isSupportedDocument(name string) bool {
	return isMarkdown(name) || strings.EqualFold(filepath.Ext(name), ".pdf")
}

type pdfMetadata struct {
	Title, Authors, Keywords, Text string
	Year, PageCount                int
}

var pdfYearPattern = regexp.MustCompile(`(?:D:)?([12][0-9]{3})`)

// extractPDF reads embedded metadata and a rebuildable plain-text projection.
// The PDF bytes remain authoritative on disk and are never stored in FTS.
func extractPDF(absolutePath string) (metadata pdfMetadata, err error) {
	base := filepath.Base(absolutePath)
	metadata.Title = strings.TrimSuffix(base, filepath.Ext(base))
	defer func() {
		if recover() != nil {
			// Unsupported PDF features must not make the filesystem entity
			// disappear from the catalog; filename metadata remains searchable.
			err = nil
		}
	}()
	file, reader, err := pdfreader.Open(absolutePath)
	if err != nil {
		return metadata, nil
	}
	defer file.Close()

	info := reader.Trailer().Key("Info")
	metadata.Title = strings.TrimSpace(info.Key("Title").Text())
	metadata.Authors = strings.TrimSpace(info.Key("Author").Text())
	metadata.Keywords = strings.TrimSpace(info.Key("Keywords").Text())
	for _, date := range []string{info.Key("CreationDate").Text(), info.Key("ModDate").Text()} {
		if match := pdfYearPattern.FindStringSubmatch(date); len(match) == 2 {
			metadata.Year, _ = strconv.Atoi(match[1])
			break
		}
	}
	metadata.PageCount = reader.NumPage()
	plain, textErr := reader.GetPlainText()
	if textErr == nil && plain != nil {
		// Keep FTS rows bounded. The source remains available for a future
		// re-extraction strategy or OCR pipeline.
		text, readErr := io.ReadAll(io.LimitReader(plain, 16<<20))
		if readErr == nil {
			metadata.Text = string(text)
		}
	}
	if metadata.Title == "" {
		metadata.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return metadata, nil
}

func extractTitle(body []byte, absolutePath string) string {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	inFrontmatter := false
	inCodeFence := false
	codeFence := byte(0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		if lineNumber == 1 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
				continue
			}
			if title, ok := frontmatterTitle(line); ok {
				return title
			}
			continue
		}

		if marker := markdownFenceMarker(line); marker != 0 {
			if !inCodeFence {
				inCodeFence, codeFence = true, marker
			} else if marker == codeFence {
				inCodeFence, codeFence = false, 0
			}
			continue
		}
		if inCodeFence {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			if title := strings.TrimSpace(strings.TrimPrefix(line, "# ")); title != "" {
				return title
			}
		}
	}
	base := filepath.Base(absolutePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func frontmatterTitle(line string) (string, bool) {
	key, value, found := strings.Cut(line, ":")
	if !found || !strings.EqualFold(strings.TrimSpace(key), "title") {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || value == "~" || strings.EqualFold(value, "null") {
		return "", false
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil && strings.TrimSpace(unquoted) != "" {
			return strings.TrimSpace(unquoted), true
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func markdownFenceMarker(line string) byte {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0
	}
	marker := line[0]
	if line[1] == marker && line[2] == marker {
		return marker
	}
	return 0
}

type Reader struct{}

func (Reader) Read(ctx context.Context, absolutePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("reading document %q: %w", absolutePath, err)
	}
	return body, nil
}
