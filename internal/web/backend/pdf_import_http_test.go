package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func TestPDFImportHandlerStoresAndCatalogsDroppedPDF(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlite.Open(filepath.Join(root, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pdfRoot := filepath.Join(root, "managed-pdfs")
	if err := store.SetSetting(ctx, application.SettingPDFPath, pdfRoot); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
	server := NewServer(service, fstest.MapFS{}, fstest.MapFS{})

	request := pdfImportRequest(t, "Dropped Paper.pdf", backendTestPDFBytes())
	request.Header.Set(miruClientHeader, "1")
	response := httptest.NewRecorder()
	server.handlePDFImport(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%q", response.Code, response.Body.String())
	}
	var payload struct {
		DocumentID string `json:"document_id"`
		Filename   string `json:"filename"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DocumentID == "" || payload.Filename != "Dropped Paper.pdf" || payload.Title != "Fixture Paper" {
		t.Fatalf("unexpected import payload: %+v", payload)
	}
	stored, err := os.ReadFile(filepath.Join(pdfRoot, "Dropped Paper.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, backendTestPDFBytes()) {
		t.Fatal("managed PDF bytes differ from upload")
	}
	documents, err := service.ListDocuments(ctx, 10, false, "")
	if err != nil || len(documents) != 1 || string(documents[0].Document.ID) != payload.DocumentID {
		t.Fatalf("catalog documents=%d err=%v payload=%+v", len(documents), err, payload)
	}
}

func TestPDFImportHandlerRequiresMiruHeader(t *testing.T) {
	server := NewServer(nil, fstest.MapFS{}, fstest.MapFS{})
	request := pdfImportRequest(t, "paper.pdf", backendTestPDFBytes())
	response := httptest.NewRecorder()
	server.handlePDFImport(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestSafePDFUploadName(t *testing.T) {
	if got, err := safePDFUploadName(`C:\\Users\\Ada\\paper.pdf`); err != nil || got != "paper.pdf" {
		t.Fatalf("safe windows filename=%q err=%v", got, err)
	}
	for _, name := range []string{"", "paper.txt", ".pdf"} {
		if _, err := safePDFUploadName(name); err == nil {
			t.Fatalf("unsafe filename accepted: %q", name)
		}
	}
}

func pdfImportRequest(t *testing.T, filename string, body []byte) *http.Request {
	t.Helper()
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/pdfs/import", &encoded)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
