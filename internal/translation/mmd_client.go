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
type MMDClient struct{ SocketPath string }

func (c MMDClient) Stream(ctx context.Context, input Request, emit EmitFunc) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mmd/v1/translation/stream", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.SocketPath)
	}}}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("mmd is not running; start it with `mmd run`")
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
