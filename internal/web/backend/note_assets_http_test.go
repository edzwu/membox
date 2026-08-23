package backend

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testNotePNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestNoteImageUploadAndServe(t *testing.T) {
	home := t.TempDir()
	server := &Server{home: home}
	upload := httptest.NewRequest(http.MethodPost, "/api/note-assets", bytes.NewReader(testNotePNG))
	upload.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handleNoteImageUpload(response, upload)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.URL, "/api/note-assets/") || result.Size != int64(len(testNotePNG)) {
		t.Fatalf("result=%+v", result)
	}
	stored := filepath.Join(home, "assets", "note-images", result.Filename)
	if _, err := os.Stat(stored); err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRequest(http.MethodGet, result.URL, nil)
	served := httptest.NewRecorder()
	server.handleNoteImage(served, get)
	if served.Code != http.StatusOK || !bytes.Equal(served.Body.Bytes(), testNotePNG) {
		t.Fatalf("serve status=%d bytes=%d", served.Code, served.Body.Len())
	}
	if served.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("note image response missing nosniff")
	}
}

func TestNoteImageUploadImportsPastedAbsolutePath(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "Finder clipboard.png")
	if err := os.WriteFile(source, testNotePNG, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{home: home}
	body, _ := json.Marshal(map[string]string{"source_path": source})
	request := httptest.NewRequest(http.MethodPost, "/api/note-assets", bytes.NewReader(body))
	request.Header.Set(miruClientHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleNoteImageUpload(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("path upload status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNoteImageUploadRequiresMiruAndImageBytes(t *testing.T) {
	server := &Server{home: t.TempDir()}
	blocked := httptest.NewRecorder()
	server.handleNoteImageUpload(blocked, httptest.NewRequest(http.MethodPost, "/api/note-assets", bytes.NewReader(testNotePNG)))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("missing Miru header status=%d", blocked.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/note-assets", strings.NewReader("not an image"))
	invalid.Header.Set(miruClientHeader, "1")
	invalidResponse := httptest.NewRecorder()
	server.handleNoteImageUpload(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid image status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}

	traversal := httptest.NewRecorder()
	server.handleNoteImage(traversal, httptest.NewRequest(http.MethodGet, "/api/note-assets/../secret.png", nil))
	if traversal.Code != http.StatusNotFound {
		t.Fatalf("traversal status=%d", traversal.Code)
	}
}
