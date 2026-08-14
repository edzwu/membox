package pdfconvert

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPClientBypassesEnvironmentProxyForLANService(t *testing.T) {
	client := NewHTTPClient()
	transport, ok := client.HTTP.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("LAN converter client inherited an HTTP proxy: %#v", client.HTTP.Transport)
	}
}

func TestHTTPClientDownloadsMarkdownAndImagesAsZIP(t *testing.T) {
	markdown := "# Converted\n\n![](images/chart.jpg)\n"
	bundle := testZIP(t, map[string][]byte{
		"paper.md":         []byte(markdown),
		"images/chart.jpg": []byte("jpeg-content"),
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/convert" {
			http.NotFound(response, request)
			return
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		fields := map[string]string{}
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Error(nextErr)
				return
			}
			content, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				if part.FileName() != "paper.pdf" || string(content) != "%PDF-fixture" {
					t.Errorf("unexpected PDF part: %s %q", part.FileName(), content)
				}
				continue
			}
			fields[part.FormName()] = string(content)
		}
		for name, expected := range map[string]string{"format": "zip", "lang": "ch", "method": "auto", "effort": "medium"} {
			if fields[name] != expected {
				t.Errorf("field %s=%q, want %q", name, fields[name], expected)
			}
		}
		response.Header().Set("Content-Type", "application/zip")
		_, _ = response.Write(bundle)
	}))
	defer server.Close()

	client := NewHTTPClient()
	client.HTTP = server.Client()
	client.ConversionTimeout = time.Second
	result, err := client.Convert(context.Background(), server.URL, "paper.pdf", strings.NewReader("%PDF-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(markdown))
	if result.MarkdownSHA256 != hex.EncodeToString(digest[:]) || result.Markdown != markdown {
		t.Fatalf("unexpected conversion result: %+v", result)
	}
	if len(result.Assets) != 1 || result.Assets[0].RelativePath != "images/chart.jpg" || string(result.Assets[0].Body) != "jpeg-content" {
		t.Fatalf("converted image missing: %+v", result.Assets)
	}
}

func TestDecodeBundleRejectsPathTraversal(t *testing.T) {
	payload := testZIP(t, map[string][]byte{
		"paper.md":           []byte("# Safe\n"),
		"images/../../x.jpg": []byte("unsafe"),
	})
	_, _, err := decodeBundle(payload)
	if err == nil || !strings.Contains(err.Error(), "unsafe converted ZIP path") {
		t.Fatalf("expected path traversal rejection, got %v", err)
	}
}

func TestHTTPClientSurfacesRemoteFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(response, `{"detail":"CUDA out of memory"}`)
	}))
	defer server.Close()

	client := NewHTTPClient()
	client.HTTP = server.Client()
	client.ConversionTimeout = time.Second
	_, err := client.Convert(context.Background(), server.URL, "paper.pdf", strings.NewReader("%PDF-fixture"))
	if err == nil || !strings.Contains(err.Error(), "CUDA out of memory") {
		t.Fatalf("expected remote failure, got %v", err)
	}
}

func testZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
