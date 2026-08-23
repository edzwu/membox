package translation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// MMDClient streams translation events over mmd's owner-only Unix socket.
// When Ensure is set, a failed connection triggers on-demand startup of mmd
// and one retry, so callers never have to manage the daemon themselves.
type MMDClient struct {
	SocketPath string
	Ensure     func(ctx context.Context) error
}

// post issues one JSON request to mmd. On a connection-level failure it runs
// the optional Ensure hook and retries once (the request body is rebuilt so a
// partially consumed stream is never replayed).
func (c MMDClient) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	attempt := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mmd"+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", c.SocketPath)
		}}}
		return client.Do(req)
	}
	response, err := attempt()
	if err == nil {
		return response, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if c.Ensure != nil {
		if ensureErr := c.Ensure(ctx); ensureErr == nil {
			if retried, retryErr := attempt(); retryErr == nil {
				return retried, nil
			}
		}
	}
	return nil, fmt.Errorf("mmd is unreachable at %s: %w", c.SocketPath, err)
}

func (c MMDClient) Stream(ctx context.Context, input Request, emit EmitFunc) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	response, err := c.post(ctx, "/v1/translation/stream", body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mmd translation HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	reader := bufio.NewReader(response.Body)
	done := false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) != 0 {
			var event Event
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("decode mmd translation event: %w", err)
			}
			if event.Type == "error" {
				return errors.New(event.Text)
			}
			if event.Type == "done" {
				done = true
			}
			if emit != nil {
				if err := emit(event); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if !done {
					return errors.New("mmd translation stream ended before done")
				}
				return nil
			}
			return readErr
		}
	}
}

// StreamPrompt POSTs one prompt to mmd's streaming LLM endpoint and forwards
// NDJSON text deltas as they are generated.
func (c MMDClient) StreamPrompt(ctx context.Context, prompt string, emit func(string) error) error {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return err
	}
	response, err := c.post(ctx, "/v1/llm/stream", body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mmd llm stream HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	reader := bufio.NewReader(response.Body)
	done := false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) != 0 {
			var event struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("decode mmd llm stream event: %w", err)
			}
			switch event.Type {
			case "error":
				return errors.New(event.Text)
			case "done":
				done = true
			case "delta":
				if emit != nil {
					if err := emit(event.Text); err != nil {
						return err
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if !done {
					return errors.New("mmd llm stream ended before done")
				}
				return nil
			}
			return readErr
		}
	}
}

// Complete calls mmd's one-shot LLM endpoint (no streaming).
func (c MMDClient) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return "", err
	}
	response, err := c.post(ctx, "/v1/llm/complete", body)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("mmd completion HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode mmd completion: %w", err)
	}
	return result.Text, nil
}
