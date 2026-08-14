package pdfconvert

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
		if request.Method != http.MethodPost || request.URL.Path != "/convert/stream" {
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
		response.Header().Set("Content-Type", "application/x-ndjson")
		encoder := json.NewEncoder(response)
		_ = encoder.Encode(streamEvent{Type: "progress", Progress: Progress{Stage: "split", TotalPages: 12, InitialChunkPages: 4}})
		_ = encoder.Encode(streamEvent{Type: "heartbeat"})
		_ = encoder.Encode(streamEvent{Type: "progress", Progress: Progress{Stage: "chunk_done", PageFrom: 1, PageTo: 4, TotalPages: 12}})
		_ = encoder.Encode(streamEvent{Type: "result", Format: "zip", ZIPBase64: base64.StdEncoding.EncodeToString(bundle)})
	}))
	defer server.Close()

	client := NewHTTPClient()
	client.HTTP = server.Client()
	client.ConversionTimeout = time.Second
	var progress []Progress
	result, err := client.Convert(context.Background(), server.URL, "paper.pdf", strings.NewReader("%PDF-fixture"), func(event Progress) {
		progress = append(progress, event)
	})
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
	if len(progress) != 2 || progress[0].Stage != "split" || progress[1].PageTo != 4 {
		t.Fatalf("stream progress=%+v", progress)
	}
}

func TestHTTPClientSurfacesStreamErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(response).Encode(streamEvent{Type: "error", Progress: Progress{Detail: "worker ran out of memory"}})
	}))
	defer server.Close()
	client := NewHTTPClient()
	client.HTTP = server.Client()
	client.ConversionTimeout = time.Second
	_, err := client.Convert(context.Background(), server.URL, "paper.pdf", strings.NewReader("%PDF-fixture"), nil)
	if err == nil || !strings.Contains(err.Error(), "worker ran out of memory") {
		t.Fatalf("stream error=%v", err)
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
	_, err := client.Convert(context.Background(), server.URL, "paper.pdf", strings.NewReader("%PDF-fixture"), nil)
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
