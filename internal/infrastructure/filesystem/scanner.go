package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

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
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !isMarkdown(fullPath) {
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
		return catalog.Observation{}, fmt.Errorf("reading Markdown: %w", err)
	}
	if !utf8.Valid(body) {
		return catalog.Observation{}, errors.New("Markdown is not valid UTF-8")
	}
	if err := ctx.Err(); err != nil {
		return catalog.Observation{}, err
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return catalog.Observation{}, fmt.Errorf("stating Markdown: %w", err)
	}
	digest := sha256.Sum256(body)
	return catalog.Observation{
		Location: location,
		FileKey:  fileKey(info),
		Title:    extractTitle(body, absolutePath),
		MTime:    info.ModTime().UnixNano(),
		Size:     info.Size(),
		SHA256:   hex.EncodeToString(digest[:]),
		Body:     body,
	}, nil
}

func isMarkdown(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func extractTitle(body []byte, absolutePath string) string {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			if title := strings.TrimSpace(strings.TrimPrefix(line, "# ")); title != "" {
				return title
			}
		}
	}
	base := filepath.Base(absolutePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
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
