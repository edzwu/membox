// Package r2 implements the remote blob backend on Cloudflare R2 (ADR-002,
// docs/remote-stage1.md). Keys are caller-chosen; document blobs are addressed
// by content SHA-256, matching the local content-addressed blobstore
// semantics. Objects are treated as immutable: metadata commits only after a
// successful Put, and Delete exists for GC/smoke cleanup, never for document
// history.
package r2

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"membox/internal/application/port"
)

// Config is the all-or-nothing R2 credential set (validated by
// internal/interfaces/httpserver.Load before Open is called).
type Config struct {
	AccountID       string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// Store is a port.RemoteContentStore backed by one private R2 bucket.
type Store struct {
	client *s3.Client
	bucket string
}

// Open builds the S3-compatible client. It performs no network I/O; the first
// failing operation surfaces credential or network problems.
func Open(cfg Config) (*Store, error) {
	if cfg.AccountID == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("r2: account ID, bucket, access key ID and secret access key are all required")
	}
	client := s3.NewFromConfig(aws.Config{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)),
	}, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Put uploads size bytes from body under key. A successful return means the
// object is durable and immediately readable.
func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("r2 put %s: %w", key, err)
	}
	return nil
}

// Get streams the object body; the caller closes it.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("r2 get %s: %w", key, err)
	}
	return out.Body, nil
}

// Head reports object metadata; exists=false means the key is absent.
func (s *Store) Head(ctx context.Context, key string) (port.RemoteObject, bool, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		var noKey *types.NoSuchKey
		if errors.As(err, &notFound) || errors.As(err, &noKey) {
			return port.RemoteObject{}, false, nil
		}
		return port.RemoteObject{}, false, fmt.Errorf("r2 head %s: %w", key, err)
	}
	return port.RemoteObject{Key: key, Size: aws.ToInt64(out.ContentLength)}, true, nil
}

// Delete removes the object. Deleting a missing key succeeds (S3 semantics).
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("r2 delete %s: %w", key, err)
	}
	return nil
}

// List enumerates up to limit objects under prefix. It exists for smoke tests
// and ops debugging only — production document listings come from PostgreSQL
// (plan T05), never from ListObjects.
func (s *Store) List(ctx context.Context, prefix string, limit int32) ([]port.RemoteObject, error) {
	out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("r2 list %s: %w", prefix, err)
	}
	objects := make([]port.RemoteObject, 0, len(out.Contents))
	for _, item := range out.Contents {
		objects = append(objects, port.RemoteObject{
			Key:  aws.ToString(item.Key),
			Size: aws.ToInt64(item.Size),
		})
	}
	return objects, nil
}
