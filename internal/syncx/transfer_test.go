package syncx_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"membox/internal/syncx"
)

// countingStore wraps a BlobStore and counts Get calls, proving zero-transfer
// behavior instead of assuming it.
type countingStore struct {
	inner syncx.BlobStore
	gets  int
}

func (c *countingStore) Has(d string) bool { return c.inner.Has(d) }
func (c *countingStore) Get(d string) (io.ReadCloser, error) {
	c.gets++
	return c.inner.Get(d)
}
func (c *countingStore) Put(d string, r io.Reader) error { return c.inner.Put(d, r) }

func TestTransferMovesBytes(t *testing.T) {
	t.Parallel()
	src, dst := newStore(t), newStore(t)
	body := []byte("# transfer me\n")
	d := putOK(t, src, body)

	moved, err := syncx.Transfer(src, dst, d)
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("empty dst: transfer should have happened")
	}
	r, err := dst.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("dst content mismatch: %q", got)
	}
}

func TestTransferHashHitMovesNothing(t *testing.T) {
	t.Parallel()
	srcInner, dst := newStore(t), newStore(t)
	body := []byte("already there")
	d := putOK(t, srcInner, body)
	putOK(t, dst, body)

	src := &countingStore{inner: srcInner}
	moved, err := syncx.Transfer(src, dst, d)
	if err != nil {
		t.Fatal(err)
	}
	if moved {
		t.Fatal("dst already has the digest: must report zero-transfer")
	}
	if src.gets != 0 {
		t.Fatalf("zero-transfer violated: src.Get called %d times", src.gets)
	}
}

func TestTransferDuplicateDocumentsShareOneTransfer(t *testing.T) {
	t.Parallel()
	src, dst := newStore(t), newStore(t)
	body := []byte("two documents, one blob")
	d := putOK(t, src, body)

	for i, want := range []bool{true, false} {
		moved, err := syncx.Transfer(src, dst, d)
		if err != nil {
			t.Fatal(err)
		}
		if moved != want {
			t.Fatalf("transfer %d: moved = %v, want %v", i, moved, want)
		}
	}
}

func TestTransferIgnoresStagingOrphans(t *testing.T) {
	t.Parallel()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	src, err := syncx.NewLocalBlobStore(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := syncx.NewLocalBlobStore(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("orphans do not block delivery")
	d := putOK(t, src, body)

	// A leftover temp file from a crashed previous round.
	if err := os.WriteFile(filepath.Join(dstDir, "blobs", ".tmp-deadbeef"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := syncx.Transfer(src, dst, d)
	if err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
}

// failAfterReader errors after n bytes, simulating a crash mid-transfer.
type failAfterReader struct {
	r    io.Reader
	left int
}

func (f *failAfterReader) Read(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errors.New("simulated mid-transfer crash")
	}
	if len(p) > f.left {
		p = p[:f.left]
	}
	n, err := f.r.Read(p)
	f.left -= n
	return n, err
}

func TestTransferCrashLeavesNoPartialStateAndRetryConverges(t *testing.T) {
	t.Parallel()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	src, err := syncx.NewLocalBlobStore(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := syncx.NewLocalBlobStore(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("crash halfway through this blob body")
	d := putOK(t, src, body)

	// Crash after 5 bytes: dst must show nothing committed.
	err = dst.Put(d, &failAfterReader{r: bytes.NewReader(body), left: 5})
	if err == nil {
		t.Fatal("crashed Put must error")
	}
	if dst.Has(d) {
		t.Fatal("crashed Put must leave no committed blob")
	}

	// Retry the round: converges.
	moved, err := syncx.Transfer(src, dst, d)
	if err != nil || !moved {
		t.Fatalf("retry: moved=%v err=%v", moved, err)
	}
	r, err := dst.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("after retry: %q", got)
	}
}

func TestTransferCorruptSourceNeverEvictsGoodBytes(t *testing.T) {
	t.Parallel()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	src, err := syncx.NewLocalBlobStore(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := syncx.NewLocalBlobStore(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("good bytes on both sides")
	d := putOK(t, src, body)
	putOK(t, dst, body)

	// Source rots: delivery verification must fail the read...
	if err := os.WriteFile(filepath.Join(srcDir, "blobs", d), []byte("rotten"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := src.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(r)
	_ = r.Close()
	if !errors.Is(readErr, syncx.ErrDigestMismatch) {
		t.Fatalf("corrupt src read: err = %v, want ErrDigestMismatch", readErr)
	}

	// ...and even a transport that bypasses source verification cannot poison
	// dst: Put re-verifies and never touches the committed good copy.
	if err := dst.Put(d, bytes.NewReader([]byte("rotten"))); !errors.Is(err, syncx.ErrDigestMismatch) {
		t.Fatalf("dst.Put of corrupt stream: err = %v, want ErrDigestMismatch", err)
	}
	r, err = dst.Get(d)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if !bytes.Equal(got, body) {
		t.Fatal("dst good bytes were evicted by a corrupt transfer")
	}
}
