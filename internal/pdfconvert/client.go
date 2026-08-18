package pdfconvert

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// PDFCONV_TIMEOUT (minutes) overrides the per-request conversion deadline.
	// Default 30 min is fine for most PDFs; multi-hundred-page books split into
	// many small VLM-safe chunks can take well over an hour.
	defaultConversionTimeout = 30 * time.Minute
	maxErrorBody             = 64 << 10
	maxZIPResponse           = 512 << 20
	maxStreamResponse        = (maxZIPResponse+2)/3*4 + 8<<20
	maxMarkdownBytes         = 64 << 20
	maxAssetBytes            = 128 << 20
	maxBundleBytes           = 1 << 30
	maxBundleEntries         = 10_000
)

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".svg": true,
	".webp": true, ".bmp": true, ".gif": true,
}

// HTTPClient implements the converter's stateless POST /convert/stream
// protocol. One request owns upload, conversion, and NDJSON progress/result
// events; the server does not retain documents and membox remains the sole
// persistence authority.
type HTTPClient struct {
	HTTP              *http.Client
	ConversionTimeout time.Duration
}

func NewHTTPClient() *HTTPClient {
	timeout := defaultConversionTimeout
	if minutes, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PDFCONV_TIMEOUT"))); err == nil && minutes > 0 {
		timeout = time.Duration(minutes) * time.Minute
	}
	return &HTTPClient{HTTP: newLANHTTPClient(), ConversionTimeout: timeout}
}

func newLANHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// This optional feature targets a LAN service. Do not inherit shell proxy
	// variables (the user's global HTTP proxy may not route private subnets).
	transport.Proxy = nil
	// Conversion duration is governed by the request context, not a shorter
	// per-client timeout that would abort MinerU while it is still working.
	return &http.Client{Transport: transport}
}

func (c *HTTPClient) Convert(ctx context.Context, serverURL, filename string, body io.Reader, onProgress func(Progress)) (RemoteResult, error) {
	if c == nil {
		return RemoteResult{}, errors.New("PDF converter HTTP client is nil")
	}
	if err := ValidateServerURL(serverURL); err != nil {
		return RemoteResult{}, err
	}
	if body == nil {
		return RemoteResult{}, errors.New("PDF upload body is required")
	}
	filename = filepath.Base(strings.TrimSpace(filename))
	if !strings.EqualFold(filepath.Ext(filename), ".pdf") {
		return RemoteResult{}, fmt.Errorf("PDF upload filename must end in .pdf: %q", filename)
	}
	client := c.HTTP
	if client == nil {
		client = newLANHTTPClient()
	}
	timeout := c.ConversionTimeout
	if timeout <= 0 {
		timeout = defaultConversionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	go func() {
		err := writeConvertMultipart(writer, filename, body)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		_ = pipeWriter.CloseWithError(err)
	}()

	endpoint := strings.TrimRight(serverURL, "/") + "/convert/stream"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pipeReader)
	if err != nil {
		_ = pipeReader.Close()
		return RemoteResult{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		return RemoteResult{}, fmt.Errorf("converting PDF: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RemoteResult{}, responseError("converting PDF", response)
	}
	payload, err := readStreamResult(response.Body, onProgress)
	if err != nil {
		return RemoteResult{}, err
	}
	markdown, assets, err := decodeBundle(payload)
	if err != nil {
		return RemoteResult{}, err
	}
	digest := sha256.Sum256([]byte(markdown))
	return RemoteResult{
		Filename:       filename,
		Markdown:       markdown,
		MarkdownSHA256: hex.EncodeToString(digest[:]),
		Assets:         assets,
	}, nil
}

type streamEvent struct {
	Progress
	Type      string `json:"type"`
	Format    string `json:"format,omitempty"`
	ZIPBase64 string `json:"zip_base64,omitempty"`
}

func readStreamResult(reader io.Reader, onProgress func(Progress)) ([]byte, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maxStreamResponse+1))
	for {
		var event streamEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("converter stream ended before a result event")
			}
			return nil, fmt.Errorf("reading converter stream event: %w", err)
		}
		switch event.Type {
		case "heartbeat":
			continue
		case "progress":
			if onProgress != nil {
				onProgress(event.Progress)
			}
		case "error":
			detail := strings.TrimSpace(event.Detail)
			if detail == "" {
				detail = "unknown converter error"
			}
			return nil, errors.New(detail)
		case "result":
			if event.Format != "zip" {
				return nil, fmt.Errorf("converter stream returned %q, want zip", event.Format)
			}
			if event.ZIPBase64 == "" {
				return nil, errors.New("converter stream returned an empty ZIP result")
			}
			decoded := base64.NewDecoder(base64.StdEncoding, strings.NewReader(event.ZIPBase64))
			payload, err := io.ReadAll(io.LimitReader(decoded, maxZIPResponse+1))
			if err != nil {
				return nil, fmt.Errorf("decoding converted ZIP: %w", err)
			}
			if len(payload) > maxZIPResponse {
				return nil, fmt.Errorf("converted ZIP exceeds %d bytes", maxZIPResponse)
			}
			return payload, nil
		}
	}
}

func writeConvertMultipart(writer *multipart.Writer, filename string, body io.Reader) error {
	fields := map[string]string{
		"format": "zip",
		"lang":   "ch",
		"method": "auto",
		"effort": "medium",
	}
	for _, name := range []string{"format", "lang", "method", "effort"} {
		if err := writer.WriteField(name, fields[name]); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, body)
	return err
}

func decodeBundle(payload []byte) (string, []Asset, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", nil, fmt.Errorf("opening converted ZIP: %w", err)
	}
	if len(reader.File) > maxBundleEntries {
		return "", nil, fmt.Errorf("converted ZIP has too many entries: %d", len(reader.File))
	}
	var markdown string
	var assets []Asset
	var total uint64
	for _, entry := range reader.File {
		name := entry.Name
		clean := path.Clean(name)
		if clean != name || strings.HasPrefix(name, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return "", nil, fmt.Errorf("unsafe converted ZIP path %q", name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		total += entry.UncompressedSize64
		if total > maxBundleBytes {
			return "", nil, fmt.Errorf("converted ZIP expands beyond %d bytes", maxBundleBytes)
		}
		switch {
		case !strings.Contains(name, "/") && strings.EqualFold(path.Ext(name), ".md"):
			if markdown != "" {
				return "", nil, errors.New("converted ZIP contains multiple Markdown files")
			}
			body, err := readZIPEntry(entry, maxMarkdownBytes)
			if err != nil {
				return "", nil, err
			}
			markdown = string(body)
		case strings.HasPrefix(name, "images/") && strings.Count(name, "/") == 1 && imageExtensions[strings.ToLower(path.Ext(name))]:
			body, err := readZIPEntry(entry, maxAssetBytes)
			if err != nil {
				return "", nil, err
			}
			assets = append(assets, Asset{RelativePath: name, Body: body})
		default:
			return "", nil, fmt.Errorf("unexpected converted ZIP entry %q", name)
		}
	}
	if strings.TrimSpace(markdown) == "" {
		return "", nil, errors.New("converted ZIP contains no Markdown")
	}
	return markdown, assets, nil
}

func readZIPEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("converted ZIP entry %q exceeds %d bytes", entry.Name, limit)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("opening converted ZIP entry %q: %w", entry.Name, err)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading converted ZIP entry %q: %w", entry.Name, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("converted ZIP entry %q exceeds %d bytes", entry.Name, limit)
	}
	return body, nil
}

func responseError(action string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	message := strings.TrimSpace(string(body))
	var detail struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(body, &detail) == nil && detail.Detail != nil {
		message = fmt.Sprint(detail.Detail)
	}
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("%s: HTTP %d: %s", action, response.StatusCode, message)
}
