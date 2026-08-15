package translation

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestMMDClientStartsDaemonOnDemandAndRetries(t *testing.T) {
	// macOS caps unix socket paths at ~104 bytes; keep the temp dir short.
	dir, err := os.MkdirTemp("", "mmdc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "mmd.sock")
	launched := false
	ensure := func(context.Context) error {
		launched = true
		listener, err := net.Listen("unix", socket)
		if err != nil {
			return err
		}
		server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/v1/llm/complete" {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]string{"text": "planned"})
		})}
		t.Cleanup(func() { _ = server.Close() })
		go func() { _ = server.Serve(listener) }()
		return nil
	}

	client := MMDClient{SocketPath: socket, Ensure: ensure}
	text, err := client.Complete(context.Background(), "plan the chapters")
	if err != nil {
		t.Fatal(err)
	}
	if text != "planned" || !launched {
		t.Fatalf("text=%q launched=%v", text, launched)
	}
}

func TestMMDClientWithoutEnsureReportsUnreachable(t *testing.T) {
	dir, err := os.MkdirTemp("", "mmdc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	client := MMDClient{SocketPath: filepath.Join(dir, "absent.sock")}
	if _, err := client.Complete(context.Background(), "x"); err == nil {
		t.Fatal("expected unreachable error")
	}
}
