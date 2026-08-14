package backend

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPathWithinRejectsSiblingAndParentPaths(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "library", ".membox-assets", "pdf-id")
	if !pathWithin(root, filepath.Join(root, "images", "chart.jpg")) {
		t.Fatal("valid nested asset path was rejected")
	}
	for _, target := range []string{
		filepath.Join(filepath.Dir(root), "other", "images", "chart.jpg"),
		filepath.Dir(root),
		filepath.Join(string(filepath.Separator), "etc", "passwd"),
	} {
		if pathWithin(root, target) {
			t.Fatalf("escaped asset path was accepted: %s", target)
		}
	}
}

func TestBridgeTokenSurvivesCorruptRuntimeFile(t *testing.T) {
	home := t.TempDir()
	first, err := LoadOrCreateBridgeToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BridgeFilePath(home), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateBridgeToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("durable token changed after corrupt bridge.json: %q → %q", first, second)
	}
}

func TestWriteBridgeFileNeverExposesPartialJSON(t *testing.T) {
	home := t.TempDir()
	if _, err := WriteBridgeFile(home, BridgeFile{BaseURL: "http://127.0.0.1:8787", Token: "token", Port: 8787}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if _, err := ReadBridgeFile(home); err != nil {
				select {
				case errorsSeen <- err:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		if _, err := WriteBridgeFile(home, BridgeFile{BaseURL: "http://127.0.0.1:8787", Token: "token", Port: 8787, PID: i}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	select {
	case err := <-errorsSeen:
		t.Fatalf("reader observed partial bridge.json: %v", err)
	default:
	}
	info, err := os.Stat(BridgeFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bridge permissions = %o, want 600", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(home, ".membox-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary bridge files leaked: %v, %v", matches, err)
	}
}
