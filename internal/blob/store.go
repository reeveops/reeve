package blob

import (
	"context"
	"io"
)

// Store is the minimal blob interface. Adapters (filesystem, s3, gcs,
// azblob) satisfy it. Consumers may define smaller use-site interfaces
// over it.
type Store interface {
	// Get reads an object. Returns ErrNotFound if absent.
	Get(ctx context.Context, key string) (io.ReadCloser, *Metadata, error)
	// Put writes an object unconditionally.
	Put(ctx context.Context, key string, r io.Reader) (*Metadata, error)
	// PutIfMatch writes only if the object's current ETag matches. Returns
	// ErrPreconditionFailed on mismatch. If ifMatch is empty, writes only
	// if the object does not exist (If-None-Match:*).
	PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*Metadata, error)
	// Delete removes an object. Missing is not an error.
	Delete(ctx context.Context, key string) error
	// List returns EVERY key under the prefix, recursively - there is no
	// "/" delimiter grouping. All adapters and consumers (drift state
	// walks, lock reaping, pending-event scans) rely on the full flat
	// enumeration.
	List(ctx context.Context, prefix string) ([]string, error)
}

// Metadata is returned by Get/Put for conditional-write loops.
type Metadata struct {
	ETag         string
	LastModified int64 // unix seconds; may be 0 if unknown
	Size         int64
}
