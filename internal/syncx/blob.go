package syncx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// BlobStore is the byte layer of the sync engine: content-addressed, lazy,
// disposable. Bytes have no name, only their digest. Deliveries are always
// verified against the digest — a cache miss is the worst case, never wrong
// bytes. Two real implementations are expected (per the "interfaces are
// earned" rule): this local CAS, and a future HTTP store for the server.
type BlobStore interface {
	Has(sha256 string) bool
	// Get returns the blob's bytes. Delivery is verified: a store whose
	// on-disk bytes no longer match the digest must fail the read, never
	// return corrupt content.
	Get(sha256 string) (io.ReadCloser, error)
	// Put stores r's bytes under the digest, verifying they actually hash
	// to it. Atomic: observers see the old state or the new state, never a
	// partial write. Idempotent.
	Put(sha256 string, r io.Reader) error
}

// ErrDigestMismatch means the bytes did not hash to the claimed digest.
var ErrDigestMismatch = errors.New("syncx: digest mismatch")

// validateDigest guards the store entrance: a digest must be 64 lowercase
// hex chars before it is used as a filename, which makes path traversal
// ("../...") impossible by construction. Pure validation — no conversion.
func validateDigest(digest string) error {
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf("syncx: bad digest %q", digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("syncx: bad digest %q", digest)
	}
	return nil
}

// LocalBlobStore is a BlobStore backed by a directory: blobs/<sha256> are the
// committed objects; writes stage in the same directory (hidden temp file)
// and are renamed into place, so commits are atomic on any filesystem that
// has atomic rename.
type LocalBlobStore struct {
	dir string
}

// NewLocalBlobStore opens (creating if needed) a CAS store rooted at dir.
func NewLocalBlobStore(dir string) (*LocalBlobStore, error) {
	// 0o700: blobs are private document content; even the digest listing
	// should not leak to other users on the machine.
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	return &LocalBlobStore{dir: dir}, nil
}

func (s *LocalBlobStore) path(digest string) (string, error) {
	if err := validateDigest(digest); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "blobs", digest), nil
}

func (s *LocalBlobStore) Has(digest string) bool {
	p, err := s.path(digest)
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// verifyOnRead hashes the stream as it is consumed and fails at EOF if the
// content does not match the digest.
type verifyOnRead struct {
	r      io.ReadCloser
	h      hash.Hash
	digest string
	done   bool
}

func (v *verifyOnRead) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	_, _ = v.h.Write(p[:n])
	if err == io.EOF && !v.done {
		v.done = true
		if hex.EncodeToString(v.h.Sum(nil)) != v.digest {
			return 0, ErrDigestMismatch
		}
	}
	return n, err
}

func (v *verifyOnRead) Close() error { return v.r.Close() }

func (s *LocalBlobStore) Get(digest string) (io.ReadCloser, error) {
	p, err := s.path(digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return &verifyOnRead{r: f, h: sha256.New(), digest: digest}, nil
}

func (s *LocalBlobStore) Put(digest string, r io.Reader) error {
	final, err := s.path(digest)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(final), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		return cleanup(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return cleanup(fmt.Errorf("%w: put %s", ErrDigestMismatch, digest))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}
	// Commit point: atomic rename. A crash before this leaves only a hidden
	// temp orphan; a crash after leaves the complete object.
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
