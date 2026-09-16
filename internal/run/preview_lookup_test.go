package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/summary"
)

// putManifest seeds a preview manifest with an explicit run id and
// created_at so tests can construct the same-second collision that the
// RunID tie-break exists to resolve.
func putManifest(t *testing.T, store blob.Store, pr int, runID, sha, createdAt string, stacks []summary.StackSummary) {
	t.Helper()
	data, err := json.Marshal(manifest{
		RunID:     runID,
		PR:        pr,
		CommitSHA: sha,
		Op:        "preview",
		CreatedAt: createdAt,
		Stacks:    stacks,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("runs/pr-%d/%s/manifest.json", pr, runID)
	if _, err := store.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}

func putRawManifest(t *testing.T, store blob.Store, pr int, runID, body string) {
	t.Helper()
	key := fmt.Sprintf("runs/pr-%d/%s/manifest.json", pr, runID)
	if _, err := store.Put(t.Context(), key, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func TestFindPreviewForStack_NoManifest(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	got, err := FindPreviewForStack(ctx, store, 42, "abc1234", "api/prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.Found {
		t.Fatalf("expected not-found on empty bucket: %+v", got)
	}
}

func TestFindPreviewForStack_MatchingManifest(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())

	// Seed a preview manifest via the regular writer path.
	stacks := []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod",
			Counts: summary.Counts{Add: 2, Change: 1},
			Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod",
			Status: summary.StatusError, Error: "engine crashed"},
	}
	if err := writeManifest(ctx, store, 42, "run-1-abc1234", stacks, "abc1234xyz"); err != nil {
		t.Fatal(err)
	}

	// Hit for api/prod → succeeded + changes.
	got, err := FindPreviewForStack(ctx, store, 42, "abc1234xyz", "api/prod")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || !got.Succeeded || !got.HasChanges {
		t.Fatalf("api/prod: unexpected: %+v", got)
	}
	// Plan must carry the stored preview summary so policy hooks evaluate the
	// real plan at apply time rather than an empty pre-apply summary.
	if got.Plan == nil {
		t.Fatal("api/prod: expected Plan to be populated")
	}
	if got.Plan.Counts.Add != 2 || got.Plan.Counts.Change != 1 {
		t.Fatalf("api/prod: Plan counts not preserved: %+v", got.Plan.Counts)
	}

	// Hit for worker/prod → found but not succeeded.
	got, err = FindPreviewForStack(ctx, store, 42, "abc1234xyz", "worker/prod")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || got.Succeeded {
		t.Fatalf("worker/prod: expected found+failed: %+v", got)
	}
	if !strings.Contains(got.ErrorMessage, "crashed") {
		t.Fatalf("expected error message preserved: %q", got.ErrorMessage)
	}

	// Miss: wrong SHA.
	got, err = FindPreviewForStack(ctx, store, 42, "different-sha", "api/prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.Found {
		t.Fatalf("expected miss on mismatched sha: %+v", got)
	}

	// Miss: stack not in manifest.
	got, err = FindPreviewForStack(ctx, store, 42, "abc1234xyz", "ghost/prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.Found {
		t.Fatalf("expected miss on absent stack: %+v", got)
	}
}

// TestPlanSucceededAgreesWithNewestManifest pins PlanSucceededForPR to the
// same manifest newestPreviewManifest considers authoritative. created_at is
// RFC3339 at second granularity, so two runs for one SHA in the same second
// collide routinely (a retried workflow, two quick pushes); the tie-break is
// RunID. PlanSucceededForPR used to re-implement the scan without that
// tie-break, so `reeve ready` could report a green plan from one run while
// apply gated against a different one.
func TestPlanSucceededAgreesWithNewestManifest(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())

	const sha = "abc1234xyz"
	const sameSecond = "2026-08-08T12:00:00Z"

	// Lower RunID: clean plan. Higher RunID: a stack errored. Same second.
	putManifest(t, store, 42, "run-1", sha, sameSecond, []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned,
			Counts: summary.Counts{Add: 1}},
	})
	putManifest(t, store, 42, "run-2", sha, sameSecond, []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusError, Error: "engine crashed"},
	})

	best, err := newestPreviewManifest(ctx, store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if best == nil {
		t.Fatal("expected a manifest")
	}
	if best.RunID != "run-2" {
		t.Fatalf("tie-break should pick the higher run id, got %q", best.RunID)
	}

	// The authoritative manifest has a failed stack, so this must be false.
	// If it reports true, the two selections disagree.
	succeeded, err := PlanSucceededForPR(ctx, store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded {
		t.Fatal("PlanSucceededForPR reported success from a manifest other than the authoritative one")
	}
}

type previewListCounter struct {
	blob.Store
	lists map[string]int
}

type previewFailureStore struct {
	blob.Store
	failList bool
	failGet  bool
}

func (s *previewFailureStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.failList {
		return nil, errors.New("list unavailable")
	}
	return s.Store.List(ctx, prefix)
}

func (s *previewFailureStore) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	if s.failGet && strings.HasSuffix(key, "/manifest.json") {
		return nil, nil, errors.New("read unavailable")
	}
	return s.Store.Get(ctx, key)
}

func (s *previewListCounter) List(ctx context.Context, prefix string) ([]string, error) {
	s.lists[prefix]++
	return s.Store.List(ctx, prefix)
}

func TestPreviewSnapshotScansManifestsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base, _ := filesystem.New(t.TempDir())
	const sha = "abc1234xyz"
	putManifest(t, base, 42, "run-1", sha, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Status: summary.StatusPlanned},
	})
	store := &previewListCounter{Store: base, lists: map[string]int{}}

	snapshot, err := LoadPreviewSnapshot(ctx, store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if refs, ok := snapshot.StackRefs(); !ok || len(refs) != 2 {
		t.Fatalf("StackRefs() = (%v, %t), want two refs", refs, ok)
	}
	for _, ref := range []string{"api/prod", "worker/prod", "api/prod"} {
		if got := snapshot.StackStatus(ref); !got.Found {
			t.Fatalf("StackStatus(%q) = %+v, want found", ref, got)
		}
	}
	if got := store.lists["runs/pr-42/"]; got != 1 {
		t.Fatalf("manifest list calls = %d, want 1", got)
	}
}

func TestPreviewSnapshotRejectsMalformedCandidates(t *testing.T) {
	t.Parallel()
	const sha = "abc1234xyz"
	tests := []struct {
		name      string
		createdAt string
		stacks    []summary.StackSummary
	}{
		{name: "invalid timestamp", createdAt: "not-a-time", stacks: []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
		}},
		{name: "missing project", createdAt: "2026-08-08T12:00:00Z", stacks: []summary.StackSummary{
			{Stack: "prod", Status: summary.StatusPlanned},
		}},
		{name: "missing stack", createdAt: "2026-08-08T12:00:00Z", stacks: []summary.StackSummary{
			{Project: "api", Status: summary.StatusPlanned},
		}},
		{name: "invalid status", createdAt: "2026-08-08T12:00:00Z", stacks: []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusBlocked},
		}},
		{name: "duplicate stack", createdAt: "2026-08-08T12:00:00Z", stacks: []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
			{Project: "api", Stack: "prod", Status: summary.StatusNoOp},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, err := filesystem.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			putManifest(t, store, 42, "run-1", sha, tt.createdAt, tt.stacks)
			if _, err := LoadPreviewSnapshot(t.Context(), store, 42, sha); err == nil {
				t.Fatal("malformed preview manifest was accepted")
			}
		})
	}
}

func TestPreviewSnapshotRejectsUnreadableHistory(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	putRawManifest(t, store, 42, "corrupt", "{not-json")
	if _, err := LoadPreviewSnapshot(t.Context(), store, 42, "abc1234xyz"); err == nil {
		t.Fatal("corrupt preview history was ignored")
	}
}

func TestPreviewSnapshotPropagatesStorageFailures(t *testing.T) {
	t.Parallel()
	base, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sha = "abc1234xyz"
	putManifest(t, base, 42, "run-1", sha, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
	})
	for _, store := range []*previewFailureStore{
		{Store: base, failList: true},
		{Store: base, failGet: true},
	} {
		if _, err := LoadPreviewSnapshot(t.Context(), store, 42, sha); err == nil {
			t.Fatal("preview storage failure was ignored")
		}
	}
}

func TestPreviewSnapshotPreservesFailedPreview(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sha = "abc1234xyz"
	putManifest(t, store, 42, "run-1", sha, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusError, Error: "engine failed"},
	})
	snapshot, err := LoadPreviewSnapshot(t.Context(), store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	status := snapshot.StackStatus("api/prod")
	if !status.Found || status.Succeeded || snapshot.Succeeded() {
		t.Fatalf("failed preview status = %+v, snapshot success = %t", status, snapshot.Succeeded())
	}
}
