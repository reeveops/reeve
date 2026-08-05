package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
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
