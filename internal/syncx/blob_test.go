package syncx_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"membox/internal/syncx"
)

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func newStore(t *testing.T) *syncx.LocalBlobStore {
	t.Helper()
	s, err := syncx.NewLocalBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func putOK(t *testing.T, s *syncx.LocalBlobStore, body []byte) string {
	t.Helper()
	d := digestOf(body)
	if err := s.Put(d, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return d
}

func TestBlobDirectoryIsPrivate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := syncx.NewLocalBlobStore(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("blobs dir perm = %o, want 0700 (digest listing is private)", perm)
	}
}

func TestBlobRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	body := []byte("# hello\n\nblob bytes\n")
	d := putOK(t, s, body)

	if !s.Has(d) {
		t.Fatal("Has should be true after Put")
	}
	r, err := s.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestPutDigestMismatchStoresNothing(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	body := []byte("real bytes")
	wrong := digestOf([]byte("claimed"))

	err := s.Put(wrong, bytes.NewReader(body))
	if !errors.Is(err, syncx.ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if s.Has(wrong) {
		t.Fatal("rejected delivery must not be stored")
	}
}

func TestPutIsIdempotent(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	body := []byte("same content twice")
	d := putOK(t, s, body)
	if err := s.Put(d, bytes.NewReader(body)); err != nil {
		t.Fatalf("second Put should be a no-op: %v", err)
	}
}

func TestGetMissingBlobErrors(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	if _, err := s.Get(digestOf([]byte("absent"))); err == nil {
		t.Fatal("Get of unknown digest must error, not return empty content")
	}
}

func TestDigestMustBeUsableAsFilename(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	for _, bad := range []string{"../escape", "not-hex", ""} {
		if err := s.Put(bad, bytes.NewReader(nil)); err == nil {
			t.Fatalf("digest %q must be rejected", bad)
		}
		if s.Has(bad) {
			t.Fatalf("digest %q must not resolve to a file", bad)
		}
	}
}

func TestGetDetectsCorruption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := syncx.NewLocalBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("bytes that will rot on disk")
	d := putOK(t, s, body)

	// Simulate bit rot: overwrite the committed object behind the store's
	// back. The layout (blobs/<digest>) is the documented on-disk contract.
	if err := os.WriteFile(filepath.Join(dir, "blobs", d), []byte("rotten"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := s.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	_ = r.Close()
	if !errors.Is(err, syncx.ErrDigestMismatch) {
		t.Fatalf("corrupted blob read: err = %v, want ErrDigestMismatch", err)
	}
}
