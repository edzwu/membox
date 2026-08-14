package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/application"
)

const (
	maxPDFUploadBytes  int64 = 500 * 1024 * 1024
	maxPDFRequestBytes int64 = maxPDFUploadBytes + 1024*1024
	miruClientHeader         = "X-Membox-Miru"
)

// handlePDFImport accepts a browser File upload, then delegates the durable
// copy, metadata extraction, and catalog update to the existing PDF import
// application service. The temporary upload never becomes catalog state.
func (s *Server) handlePDFImport(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Multipart/form-data can be sent cross-origin without a CORS preflight.
	// Requiring a custom header makes a hostile webpage preflight first; the
	// server only grants CORS to the trusted extension origins and does not
	// allow this header there.
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "same-origin Miru request required", http.StatusForbidden)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxPDFRequestBytes)
	if err := request.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(writer, "invalid or oversized PDF upload", status)
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	upload, header, err := request.FormFile("file")
	if err != nil {
		http.Error(writer, "missing PDF file", http.StatusBadRequest)
		return
	}
	defer upload.Close()
	if header.Size <= 0 || header.Size > maxPDFUploadBytes {
		http.Error(writer, "PDF must be between 1 byte and 500 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	filename, err := safePDFUploadName(header.Filename)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	temporaryRoot, err := os.MkdirTemp("", "membox-pdf-upload-*")
	if err != nil {
		http.Error(writer, "creating temporary PDF upload", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(temporaryRoot)
	temporaryPath := filepath.Join(temporaryRoot, filename)
	if err := writeUploadedPDF(temporaryPath, upload); err != nil {
		status := http.StatusBadRequest
		if !strings.Contains(err.Error(), "exceeds") {
			status = http.StatusInternalServerError
		}
		http.Error(writer, err.Error(), status)
		return
	}

	result, err := s.service.ImportPDF(request.Context(), application.ImportPDFOptions{SourcePath: temporaryPath})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"document_id": string(result.Document.ID),
		"title":       result.Document.Index.Title,
		"filename":    filepath.Base(result.Path),
		"size":        result.Document.Index.Size,
	})
}

func safePDFUploadName(value string) (string, error) {
	// filepath.Base on Unix does not treat a Windows backslash as a separator.
	value = filepath.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	extension := filepath.Ext(value)
	if value == "" || value == "." || !strings.EqualFold(extension, ".pdf") || strings.TrimSuffix(value, extension) == "" {
		return "", fmt.Errorf("uploaded file must have a .pdf filename")
	}
	return value, nil
}

func writeUploadedPDF(target string, source io.Reader) error {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating temporary PDF: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxPDFUploadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("reading PDF upload: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing temporary PDF: %w", closeErr)
	}
	if written > maxPDFUploadBytes {
		return fmt.Errorf("PDF upload exceeds 500 MiB")
	}
	return nil
}
