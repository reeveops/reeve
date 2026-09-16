package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
)

func TestResolveRetention(t *testing.T) {
	tests := []struct {
		name    string
		cfg     string
		wantDur time.Duration
		wantOn  bool
	}{
		{"default unset", "", 720 * time.Hour, true},
		{"custom", "48h", 48 * time.Hour, true},
		{"disabled zero", "0", 0, false},
		{"disabled negative", "-1h", 0, false},
		{"invalid falls back", "garbage", 720 * time.Hour, true},
	}
	for _, tt := range tests {
		s := &schemas.Shared{Retention: schemas.RetentionConfig{MaxAge: tt.cfg}}
		d, on := resolveRetention(s)
		if on != tt.wantOn || (on && d != tt.wantDur) {
			t.Errorf("%s: got (%v,%v) want (%v,%v)", tt.name, d, on, tt.wantDur, tt.wantOn)
		}
	}
}

type retentionCountingStore struct {
	objects        []blob.ListedObject
	listErr        error
	deleteErr      map[string]error
	getCalls       int
	listCalls      int
	metadataCalls  int
	deleteCalls    []string
	conditionalIDs []string
}

func (s *retentionCountingStore) Get(context.Context, string) (io.ReadCloser, *blob.Metadata, error) {
	s.getCalls++
	return nil, nil, errors.New("unexpected content read")
}

func (*retentionCountingStore) Put(context.Context, string, io.Reader) (*blob.Metadata, error) {
	return nil, errors.New("unexpected put")
}

func (*retentionCountingStore) PutIfMatch(context.Context, string, io.Reader, string) (*blob.Metadata, error) {
	return nil, errors.New("unexpected conditional put")
}

func (*retentionCountingStore) Delete(context.Context, string) error {
	return errors.New("unexpected unconditional delete")
}

func (s *retentionCountingStore) List(context.Context, string) ([]string, error) {
	s.listCalls++
	return nil, errors.New("unexpected key-only list")
}

func (s *retentionCountingStore) ListMetadata(context.Context, string) ([]blob.ListedObject, error) {
	s.metadataCalls++
	return s.objects, s.listErr
}

func (s *retentionCountingStore) DeleteIfMatch(_ context.Context, key, version string) error {
	s.deleteCalls = append(s.deleteCalls, key)
	s.conditionalIDs = append(s.conditionalIDs, version)
	return s.deleteErr[key]
}

func TestPruneRunArtifactsUsesListingMetadata(t *testing.T) {
	now := time.Unix(10_000, 0)
	store := &retentionCountingStore{
		objects: []blob.ListedObject{
			{Key: "runs/old", Version: "old-v1", LastModified: 1},
			{Key: "runs/replaced", Version: "replaced-v1", LastModified: 2},
			{Key: "runs/fresh", Version: "fresh-v1", LastModified: 9_900},
			{Key: "runs/no-time", Version: "no-time-v1"},
			{Key: "runs/no-version", LastModified: 1},
		},
		deleteErr: map[string]error{"runs/replaced": blob.ErrPreconditionFailed},
	}

	deleted, err := PruneRunArtifacts(context.Background(), store, time.Hour, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if store.metadataCalls != 1 || store.listCalls != 0 || store.getCalls != 0 {
		t.Fatalf("calls: metadata=%d list=%d get=%d", store.metadataCalls, store.listCalls, store.getCalls)
	}
	wantKeys := []string{"runs/old", "runs/replaced"}
	if len(store.deleteCalls) != len(wantKeys) {
		t.Fatalf("conditional deletes = %v, want %v", store.deleteCalls, wantKeys)
	}
	for i := range wantKeys {
		if store.deleteCalls[i] != wantKeys[i] {
			t.Fatalf("conditional deletes = %v, want %v", store.deleteCalls, wantKeys)
		}
	}
	if store.conditionalIDs[0] != "old-v1" || store.conditionalIDs[1] != "replaced-v1" {
		t.Fatalf("versions = %v", store.conditionalIDs)
	}
}

func TestPruneRunArtifactsRequiresMetadataCapability(t *testing.T) {
	store := &keyOnlyRetentionStore{}
	_, err := PruneRunArtifacts(context.Background(), store, time.Hour, time.Now())
	if !errors.Is(err, errRetentionMetadataUnsupported) {
		t.Fatalf("error = %v, want metadata capability error", err)
	}
	if store.getCalls != 0 {
		t.Fatalf("content reads = %d, want 0", store.getCalls)
	}
}

type keyOnlyRetentionStore struct{ getCalls int }

func (s *keyOnlyRetentionStore) Get(context.Context, string) (io.ReadCloser, *blob.Metadata, error) {
	s.getCalls++
	return nil, nil, blob.ErrNotFound
}
func (*keyOnlyRetentionStore) Put(context.Context, string, io.Reader) (*blob.Metadata, error) {
	return nil, nil
}
func (*keyOnlyRetentionStore) PutIfMatch(context.Context, string, io.Reader, string) (*blob.Metadata, error) {
	return nil, nil
}
func (*keyOnlyRetentionStore) Delete(context.Context, string) error { return nil }
func (*keyOnlyRetentionStore) List(context.Context, string) ([]string, error) {
	return []string{"runs/old"}, nil
}

func TestPruneRunArtifacts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, _ := filesystem.New(dir)

	// Two run artifacts; one we'll age out, one fresh.
	for _, k := range []string{"runs/pr-1/old/manifest.json", "runs/pr-1/new/manifest.json"} {
		if _, err := store.Put(ctx, k, bytes.NewReader([]byte("{}"))); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate the "old" file's mtime two months.
	oldPath := filepath.Join(dir, "runs/pr-1/old/manifest.json")
	twoMonthsAgo := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, twoMonthsAgo, twoMonthsAgo); err != nil {
		t.Fatal(err)
	}

	n, err := PruneRunArtifacts(ctx, store, 720*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	// New survives, old gone.
	if _, _, err := store.Get(ctx, "runs/pr-1/new/manifest.json"); err != nil {
		t.Errorf("fresh artifact was deleted: %v", err)
	}
	if _, _, err := store.Get(ctx, "runs/pr-1/old/manifest.json"); err == nil {
		t.Errorf("stale artifact was not deleted")
	}
}

func TestPruneDisabled(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	if _, err := store.Put(ctx, "runs/pr-1/x/manifest.json", bytes.NewReader([]byte("{}"))); err != nil {
		t.Fatal(err)
	}
	n, err := PruneRunArtifacts(ctx, store, 0, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("disabled prune should be no-op: n=%d err=%v", n, err)
	}
}
