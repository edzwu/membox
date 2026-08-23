package backend

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/noteasset"
)

const maxNoteImageRequestBytes int64 = noteasset.MaxImageBytes + 64*1024

// handleNoteImageUpload stores one clipboard image outside Markdown/Git and
// returns the stable same-origin URL to embed in annotation-note Markdown.
func (s *Server) handleNoteImageUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "same-origin Miru request required", http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxNoteImageRequestBytes)
	var image noteasset.Image
	var err error
	if strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		var input struct {
			SourcePath string `json:"source_path"`
		}
		if decodeErr := json.NewDecoder(request.Body).Decode(&input); decodeErr != nil {
			http.Error(writer, "invalid note image path request", http.StatusBadRequest)
			return
		}
		image, err = noteasset.StorePath(s.home, input.SourcePath)
	} else {
		image, err = noteasset.Store(s.home, request.Body)
	}
	if err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) || strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		if strings.Contains(err.Error(), "MEMBOX_HOME") {
			status = http.StatusServiceUnavailable
		}
		http.Error(writer, err.Error(), status)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"url":        "/api/note-assets/" + image.Filename,
		"filename":   image.Filename,
		"media_type": image.MediaType,
		"sha256":     image.SHA256,
		"size":       image.Size,
	})
}

// handleNoteImage serves only content-addressed image filenames from the
// private MEMBOX_HOME asset directory; arbitrary local paths are unreachable.
func (s *Server) handleNoteImage(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filename := strings.TrimPrefix(request.URL.Path, "/api/note-assets/")
	target, err := noteasset.Resolve(s.home, filename)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	file, err := os.Open(target)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, filename, info.ModTime(), file)
}
