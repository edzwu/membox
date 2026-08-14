package backend

import (
	"bytes"
	"context"
	"fmt"
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
	"membox/internal/pdfasset"
)

func TestPDFAssetHandlerServesOnlyManagedImagePaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlite.Open(filepath.Join(root, "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})

	pdfRoot := filepath.Join(root, "pdfs")
	if err := os.MkdirAll(pdfRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(pdfRoot, "fixture.pdf")
	if err := os.WriteFile(pdfPath, backendTestPDFBytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddPath(ctx, pdfRoot); err != nil {
		t.Fatal(err)
	}
	documents, err := service.ListDocuments(ctx, 10, false, "")
	if err != nil || len(documents) != 1 {
		t.Fatalf("indexed PDFs=%d err=%v", len(documents), err)
	}
	documentID := string(documents[0].Document.ID)
	assetRoot, err := pdfasset.Root(pdfPath, documentID)
	if err != nil {
		t.Fatal(err)
	}
	imagePath, err := pdfasset.ImageTarget(assetRoot, "images/chart.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("jpeg-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(service, fstest.MapFS{}, fstest.MapFS{})
	request := httptest.NewRequest(http.MethodGet, "/api/pdf-assets/"+documentID+"/images/chart.jpg", nil)
	response := httptest.NewRecorder()
	server.handlePDFAsset(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "jpeg-fixture" || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("asset response: code=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}

	for _, url := range []string{
		"/api/pdf-assets/" + documentID + "/images/not-image.txt",
		"/api/pdf-assets/" + documentID + "/images/nested/chart.jpg",
	} {
		request := httptest.NewRequest(http.MethodGet, url, nil)
		response := httptest.NewRecorder()
		server.handlePDFAsset(response, request)
		if response.Code == http.StatusOK {
			t.Fatalf("unsafe asset URL was served: %s", url)
		}
	}
}

func backendTestPDFBytes() []byte {
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>`,
		`<< /Length 65 >>
stream
BT /F1 18 Tf 72 720 Td (Flash Attention searchable PDF body) Tj ET
endstream`,
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
		`<< /Title (Fixture Paper) /Author (Ada Lovelace) /Keywords (attention transformer) /CreationDate (D:20240801000000Z) >>`,
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
