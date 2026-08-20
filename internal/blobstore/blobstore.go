// Package blobstore abstracts S3-compatible object storage for
// content-addressed blobs (spec 021). The server holds the only
// credentials; clients receive presigned URLs with a short TTL.
package blobstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Delete and Open for a key that is not present.
var ErrNotFound = errors.New("blobstore: object not found")

// Key returns the object key for a content hash, sharded two hex characters
// deep so the orphan sweep can parallelise over 256 prefixes.
func Key(hash string) string {
	if len(hash) < 2 {
		return "blobs/" + hash
	}
	return "blobs/" + hash[:2] + "/" + hash
}

// GetOptions are the response headers a presigned GET must carry. The
// browser sees the object store's response and not ours, so these ride along
// as S3 response-* overrides rather than as headers we set.
type GetOptions struct {
	ContentType        string
	ContentDisposition string
	CacheControl       string
}

// Object is one listed key.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Store is the object-storage surface the server needs. Implementations are
// S3 (production) and Fake (tests).
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Object, error)
}
