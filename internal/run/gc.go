package run

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
)

// retentionPrefix is the blob namespace reeve owns for run artifacts:
// manifests, applied-state pointers. Lock blobs live elsewhere and are
// handled by the lock reaper, so they are out of scope here.
const retentionPrefix = "runs/"

var errRetentionMetadataUnsupported = errors.New("blob store does not support metadata listing and conditional deletion")

type retentionStore interface {
	ListMetadata(context.Context, string) ([]blob.ListedObject, error)
	DeleteIfMatch(context.Context, string, string) error
}

// resolveRetention returns the configured max-age duration. Returns (0, false)
// when retention is disabled ("0"/negative); (d, true) otherwise, defaulting
// to one month when unset. An unparseable value falls back to the default.
func resolveRetention(s *schemas.Shared) (time.Duration, bool) {
	raw := schemas.DefaultRetentionMaxAge
	if s != nil && s.Retention.MaxAge != "" {
		raw = s.Retention.MaxAge
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid retention.max_age; using default", "value", raw, "default", schemas.DefaultRetentionMaxAge)
		d, _ = time.ParseDuration(schemas.DefaultRetentionMaxAge)
	}
	if d <= 0 {
		return 0, false
	}
	return d, true
}

// PruneRunArtifacts deletes blob items under runs/ older than maxAge. It is
// best-effort and opportunistic (called at run start, like the lock reaper):
// per-item failures are logged and skipped rather than aborting. Returns the
// count deleted.
//
// Note on PR-merge-based cleanup: reeve can't reliably know a PR's merge state
// without extra VCS wiring (webhooks or polling), so retention is purely
// age-based. A merged PR's artifacts simply age out at max_age.
func PruneRunArtifacts(ctx context.Context, store blob.Store, maxAge time.Duration, now time.Time) (int, error) {
	if store == nil || maxAge <= 0 {
		return 0, nil
	}
	maintenance, ok := store.(retentionStore)
	if !ok {
		return 0, errRetentionMetadataUnsupported
	}
	objects, err := maintenance.ListMetadata(ctx, retentionPrefix)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-maxAge)
	deleted := 0
	for _, object := range objects {
		if object.LastModified == 0 || object.Version == "" {
			continue // Incomplete metadata never authorizes deletion.
		}
		if time.Unix(object.LastModified, 0).After(cutoff) {
			continue // newer than cutoff
		}
		if err := maintenance.DeleteIfMatch(ctx, object.Key, object.Version); err != nil {
			if errors.Is(err, blob.ErrPreconditionFailed) || errors.Is(err, blob.ErrNotFound) {
				slog.Debug("retention: object changed after listing, skipping", "key", object.Key)
				continue
			}
			slog.Warn("retention: delete failed", "key", object.Key, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		slog.Info("retention: pruned old run artifacts", "count", deleted, "max_age", maxAge.String())
	}
	return deleted, nil
}

// PruneRunArtifactsOpportunistic wraps PruneRunArtifacts for call sites that
// just want best-effort cleanup with config-driven age and wall-clock now.
func PruneRunArtifactsOpportunistic(ctx context.Context, store blob.Store, s *schemas.Shared) {
	maxAge, enabled := resolveRetention(s)
	if !enabled {
		return
	}
	if _, err := PruneRunArtifacts(ctx, store, maxAge, time.Now()); err != nil {
		slog.Warn("retention: prune failed", "err", err)
	}
}
