package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"membox/internal/application/port"
)

// Store is a direct, content-addressed object store. Objects are immutable and
// addressed by the SHA-256 of their complete logical content. A later chunked
// representation can implement the same application port without changing
// Document or Version identity.
type Store struct {
	root string
}

func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("blob store root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve blob store root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, "sha256"), 0o700); err != nil {
		return nil, fmt.Errorf("create blob store: %w", err)
	}
	return &Store{root: absolute}, nil
}

func (s *Store) Put(ctx context.Context, body []byte) (port.ContentObject, error) {
	if err := ctx.Err(); err != nil {
		return port.ContentObject{}, err
	}
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	object := port.ContentObject{SHA256: hash, ObjectHash: hash, Size: int64(len(body))}
	path := s.objectPath(hash)

	if info, err := os.Stat(path); err == nil {
		if info.Size() != object.Size {
			return port.ContentObject{}, fmt.Errorf("blob %s has size %d, want %d", hash, info.Size(), object.Size)
		}
		if err := verifyObject(path, hash); err != nil {
			return port.ContentObject{}, err
		}
		return object, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return port.ContentObject{}, fmt.Errorf("inspect blob %s: %w", hash, err)
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return port.ContentObject{}, fmt.Errorf("create blob directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".membox-object-*")
	if err != nil {
		return port.ContentObject{}, fmt.Errorf("stage blob %s: %w", hash, err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return port.ContentObject{}, fmt.Errorf("secure staged blob %s: %w", hash, err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(body)); err != nil {
		return port.ContentObject{}, fmt.Errorf("write blob %s: %w", hash, err)
	}
	if err := ctx.Err(); err != nil {
		return port.ContentObject{}, err
	}
	if err := temporary.Sync(); err != nil {
		return port.ContentObject{}, fmt.Errorf("sync blob %s: %w", hash, err)
	}
	if err := temporary.Close(); err != nil {
		return port.ContentObject{}, fmt.Errorf("close blob %s: %w", hash, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return port.ContentObject{}, fmt.Errorf("publish blob %s: %w", hash, err)
	}
	published = true
	if err := syncDirectory(directory); err != nil {
		return port.ContentObject{}, fmt.Errorf("sync blob directory: %w", err)
	}
	return object, nil
}

func (s *Store) objectPath(hash string) string {
	return filepath.Join(s.root, "sha256", hash[:2], hash[2:])
}

func verifyObject(path, wantHash string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open existing blob %s: %w", wantHash, err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("verify existing blob %s: %w", wantHash, err)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != wantHash {
		return fmt.Errorf("blob %s failed SHA-256 verification (got %s)", wantHash, got)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
