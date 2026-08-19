package pdfasset

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const DirectoryName = ".membox-assets"

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".svg": true,
	".webp": true, ".bmp": true, ".gif": true,
}

// Directory returns the private asset directory under parentDir, isolated by
// stable document UUID. Binary assets stay out of Markdown/Git trees when
// parentDir is the managed PDF library (or another non-git root).
func Directory(parentDir, documentID string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(documentID))
	if err != nil {
		return "", fmt.Errorf("invalid document ID %q", documentID)
	}
	parentDir = filepath.Clean(strings.TrimSpace(parentDir))
	if parentDir == "" || parentDir == "." {
		return "", fmt.Errorf("asset parent directory is required")
	}
	return filepath.Join(parentDir, DirectoryName, parsed.String()), nil
}

// Root returns the private asset directory adjacent to a managed PDF. Assets
// are isolated by stable PDF UUID and never live in a Markdown/Git directory.
func Root(pdfPath, documentID string) (string, error) {
	pdfPath = filepath.Clean(strings.TrimSpace(pdfPath))
	if pdfPath == "" || pdfPath == "." {
		return "", fmt.Errorf("PDF path is required")
	}
	return Directory(filepath.Dir(pdfPath), documentID)
}

// ImageTarget validates one converter/API relative path and returns its path
// beneath root. Only a flat images/<filename> namespace is accepted.
func ImageTarget(root, relativePath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relativePath)))
	if filepath.ToSlash(clean) != relativePath || filepath.Dir(clean) != "images" || filepath.Base(clean) == "." || !imageExtensions[strings.ToLower(filepath.Ext(clean))] {
		return "", fmt.Errorf("unsafe PDF asset path %q", relativePath)
	}
	return filepath.Join(root, clean), nil
}
