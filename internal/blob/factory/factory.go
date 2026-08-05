// Package factory opens the right blob.Store for a given bucket config.
// Kept in its own package so adapters remain optional at import time
// (lets tests import just the filesystem adapter without pulling in
// all cloud SDKs).
package factory

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/azblob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/blob/gcs"
	"github.com/FynxLabs/reeve/internal/blob/s3"
	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// Open returns a blob.Store backed by the configured bucket.
// `root` is the repo root, used to resolve relative filesystem paths.
func Open(ctx context.Context, b schemas.BucketConfig, root string) (blob.Store, error) {
	switch b.Type {
	case "filesystem", "":
		dir := b.Name
		if dir == "" {
			return nil, fmt.Errorf("bucket.name required for filesystem backend")
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		return filesystem.New(dir)

	case "s3", "r2":
		opts := s3.Options{
			Bucket: b.Name,
			Region: b.Region,
			Prefix: b.Prefix,
		}
		if b.Type == "r2" {
			// R2 requires path-style + a custom endpoint. Users set the
			// endpoint via AWS_ENDPOINT_URL_S3 env var, or we could accept
			// it on the BucketConfig. Phase 3 keeps it env-driven.
			opts.UsePathStyle = true
		}
		return s3.New(ctx, opts)

	case "gcs":
		return gcs.New(ctx, gcs.Options{Bucket: b.Name, Prefix: b.Prefix})

	case "azblob":
		// Azure needs a service URL. The bucket.name is the container.
		// region carries the service URL in Phase 3 to avoid adding a new
		// schema field; Phase 4 will have a dedicated endpoint.
		if b.Region == "" {
			return nil, fmt.Errorf("azblob: bucket.region must be the service URL (e.g. https://<account>.blob.core.windows.net)")
		}
		return azblob.New(ctx, azblob.Options{
			ServiceURL: b.Region,
			Container:  b.Name,
			Prefix:     b.Prefix,
		})

	default:
		return nil, fmt.Errorf("unknown bucket.type %q", b.Type)
	}
}
