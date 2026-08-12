package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPutPublishesDirectContentAddressedObjectAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("# Version one\n\nImmutable content.\n")
	first, err := store.Put(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	wantHash := hex.EncodeToString(digest[:])
	if first.SHA256 != wantHash || first.ObjectHash != wantHash || first.Size != int64(len(body)) {
		t.Fatalf("object = %+v", first)
	}
	path := filepath.Join(root, "sha256", wantHash[:2], wantHash[2:])
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(body) {
		t.Fatalf("stored body = %q", stored)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	firstModTime := info.ModTime()

	second, err := store.Put(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("deduplicated object = %+v, want %+v", second, first)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(firstModTime) {
		t.Fatalf("deduplicated Put rewrote object: %s -> %s", firstModTime, info.ModTime())
	}

	corrupt := append([]byte(nil), body...)
	corrupt[len(corrupt)-2] ^= 1
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), body); err == nil {
		t.Fatal("Put accepted an existing same-size object with the wrong hash")
	}
}
