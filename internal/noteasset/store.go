// Package noteasset stores binary images referenced by annotation-note Markdown.
// Assets live under MEMBOX_HOME, outside user Markdown/Git trees, and use
// content-addressed filenames so repeated clipboard pastes deduplicate safely.
package noteasset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DirectoryName = "note-images"
	MaxImageBytes = 20 * 1024 * 1024
	assetRootMode = 0o700
	assetFileMode = 0o600
)

var (
	filenamePattern = regexp.MustCompile(`^[a-f0-9]{64}\.(?:png|jpg|gif|webp|bmp)$`)
	imageTypes      = map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/gif":  ".gif",
		"image/webp": ".webp",
		"image/bmp":  ".bmp",
	}
)

type Image struct {
	Filename  string
	Path      string
	MediaType string
	SHA256    string
	Size      int64
}

// Root is the private image store for annotation notes.
func Root(home string) (string, error) {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "" || home == "." {
		return "", errors.New("MEMBOX_HOME is required for note images")
	}
	return filepath.Join(home, "assets", DirectoryName), nil
}

// Store validates image bytes by signature, hashes them, and writes one
// content-addressed file. SVG is deliberately unsupported because note images
// are served inline by a local authenticated reader.
func Store(home string, source io.Reader) (Image, error) {
	root, err := Root(home)
	if err != nil {
		return Image{}, err
	}
	data, err := io.ReadAll(io.LimitReader(source, MaxImageBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("read note image: %w", err)
	}
	if len(data) == 0 {
		return Image{}, errors.New("note image is empty")
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("note image exceeds %d bytes", MaxImageBytes)
	}
	mediaType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	extension, ok := imageTypes[mediaType]
	if !ok {
		return Image{}, fmt.Errorf("unsupported note image type %q", mediaType)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	filename := digest + extension
	if err := os.MkdirAll(root, assetRootMode); err != nil {
		return Image{}, fmt.Errorf("create note image directory: %w", err)
	}
	target := filepath.Join(root, filename)
	result := Image{Filename: filename, Path: target, MediaType: mediaType, SHA256: digest, Size: int64(len(data))}
	if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
		return result, nil
	}

	// Publish atomically so a reader never observes a partial image. Concurrent
	// writers have identical hash-addressed bytes, so the last rename is benign.
	file, err := os.CreateTemp(root, ".note-image-*")
	if err != nil {
		return Image{}, fmt.Errorf("create temporary note image: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(assetFileMode); err != nil {
		_ = file.Close()
		return Image{}, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return Image{}, errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(temporary, target); err != nil {
		return Image{}, fmt.Errorf("publish note image: %w", err)
	}
	return result, nil
}

// StorePath imports an explicitly pasted local image path. ~/ is expanded;
// the only accepted relative form is a lone Finder filename, resolved against
// ~/Downloads (macOS "Copy … as Pathname" can expose only the basename).
func StorePath(home, sourcePath string) (Image, error) {
	sourcePath = strings.TrimSpace(strings.Trim(sourcePath, `"'`))
	userHome, homeErr := os.UserHomeDir()
	if strings.HasPrefix(sourcePath, "~/") {
		if homeErr != nil {
			return Image{}, homeErr
		}
		sourcePath = filepath.Join(userHome, strings.TrimPrefix(sourcePath, "~/"))
	}
	if !filepath.IsAbs(sourcePath) {
		if homeErr != nil || filepath.Base(sourcePath) != sourcePath || strings.ContainsAny(sourcePath, `/\\`) {
			return Image{}, errors.New("note image source path must be absolute")
		}
		sourcePath = filepath.Join(userHome, "Downloads", sourcePath)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return Image{}, fmt.Errorf("open pasted image path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Image{}, errors.New("pasted image path is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > MaxImageBytes {
		return Image{}, fmt.Errorf("note image must be between 1 and %d bytes", MaxImageBytes)
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return Image{}, fmt.Errorf("open pasted image path: %w", err)
	}
	defer file.Close()
	return Store(home, file)
}

// Resolve validates a URL filename and returns its private on-disk path.
func Resolve(home, filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if !filenamePattern.MatchString(filename) {
		return "", fmt.Errorf("invalid note image name %q", filename)
	}
	root, err := Root(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filename), nil
}
