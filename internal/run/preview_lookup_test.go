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
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/summary"
)

// putManifest seeds a preview manifest with an explicit run id and
// created_at so tests can construct the same-second collision that the
// RunID tie-break exists to resolve.
func putManifest(t *testing.T, store blob.Store, pr int, runID, sha, createdAt string, stacks []summary.StackSummary) {
	t.Helper()
	runID = testPreviewRunID(runID, sha)
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

func putRawManifest(t *testing.T, store blob.Store, pr int, runID, sha, body string) {
	t.Helper()
	runID = testPreviewRunID(runID, sha)
	key := fmt.Sprintf("runs/pr-%d/%s/manifest.json", pr, runID)
	if _, err := store.Put(t.Context(), key, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func testPreviewRunID(runID, sha string) string {
	if !strings.HasPrefix(runID, "run-") {
		runID = "run-" + runID
	}
	if strings.HasSuffix(runID, "-"+artifactSHA(sha)) || strings.HasSuffix(runID, "-"+shortSHA(sha)) {
		return runID
	}
	return runID + "-" + artifactSHA(sha)
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
	if best.RunID != testPreviewRunID("run-2", sha) {
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

func TestNewestPreviewManifestOrdersRunIdentityNumerically(t *testing.T) {
	t.Parallel()
	const sha = "abc1234xyz"
	const sameSecond = "2026-08-08T12:00:00Z"
	tests := []struct {
		name       string
		first      string
		second     string
		wantNewest string
	}{
		{name: "attempt", first: "run-9-10", second: "run-9-2", wantNewest: "run-9-10"},
		{name: "run number", first: "run-10", second: "run-9", wantNewest: "run-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, err := filesystem.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, runID := range []string{tt.first, tt.second} {
				putManifest(t, store, 42, runID, sha, sameSecond, []summary.StackSummary{
					{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
				})
			}
			best, err := newestPreviewManifest(t.Context(), store, 42, sha)
			if err != nil {
				t.Fatal(err)
			}
			if best == nil || best.RunID != testPreviewRunID(tt.wantNewest, sha) {
				t.Fatalf("newest manifest = %+v, want %q", best, testPreviewRunID(tt.wantNewest, sha))
			}
		})
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

var (
	errPreviewListUnavailable = errors.New("list unavailable")
	errPreviewReadUnavailable = errors.New("read unavailable")
)

type previewExtraKeysStore struct {
	blob.Store
	keys []string
}

func (s *previewExtraKeysStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := s.Store.List(ctx, prefix)
	return append(keys, s.keys...), err
}

func (s *previewFailureStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.failList {
		return nil, errPreviewListUnavailable
	}
	return s.Store.List(ctx, prefix)
}

func (s *previewFailureStore) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	if s.failGet && strings.HasSuffix(key, "/manifest.json") {
		return nil, nil, errPreviewReadUnavailable
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
		parseErr  bool
	}{
		{name: "invalid timestamp", createdAt: "not-a-time", stacks: []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
		}, parseErr: true},
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
			_, err = LoadPreviewSnapshot(t.Context(), store, 42, sha)
			if tt.parseErr {
				var parseErr *time.ParseError
				if !errors.As(err, &parseErr) {
					t.Fatalf("LoadPreviewSnapshot error = %v, want *time.ParseError", err)
				}
			} else if !errors.Is(err, errInvalidPreviewManifest) {
				t.Fatalf("LoadPreviewSnapshot error = %v, want %v", err, errInvalidPreviewManifest)
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
	putRawManifest(t, store, 42, "corrupt", "abc1234xyz", "{not-json")
	_, err = LoadPreviewSnapshot(t.Context(), store, 42, "abc1234xyz")
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("LoadPreviewSnapshot error = %v, want *json.SyntaxError", err)
	}
}

func TestPreviewSnapshotSkipsLegacyShortSHACollision(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const targetSHA = "abcdef1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const otherSHA = "abcdef1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	putManifest(t, store, 42, "run-9-1", targetSHA, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
	})
	putManifest(t, store, 42, "run-10-"+shortSHA(otherSHA), otherSHA, "2026-08-08T12:00:01Z", []summary.StackSummary{
		{Project: "worker", Stack: "prod", Status: summary.StatusPlanned},
	})

	snapshot, err := LoadPreviewSnapshot(t.Context(), store, 42, targetSHA)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StackStatus("api/prod").Succeeded {
		t.Fatal("full-SHA preview was not selected")
	}
	if snapshot.StackStatus("worker/prod").Found {
		t.Fatal("legacy preview for a different full SHA was selected")
	}
}

func TestPreviewSnapshotIgnoresUnrelatedMalformedManifests(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sha = "abc1234xyz"
	putManifest(t, store, 42, "current", sha, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
	})
	putRawManifest(t, store, 42, "other", "different-sha", "{not-json")
	if _, err := store.Put(t.Context(), "runs/pr-42/apply-9-1-abc1234/manifest.json", strings.NewReader("{not-json")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := LoadPreviewSnapshot(t.Context(), store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StackStatus("api/prod").Succeeded {
		t.Fatal("matching preview was not selected")
	}
}

func TestPreviewSnapshotIgnoresUnrelatedMissingManifests(t *testing.T) {
	t.Parallel()
	base, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sha = "abc1234xyz"
	putManifest(t, base, 42, "current", sha, "2026-08-08T12:00:00Z", []summary.StackSummary{
		{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
	})
	store := &previewExtraKeysStore{Store: base, keys: []string{
		"runs/pr-42/apply-9-1-abc1234/manifest.json",
		"runs/pr-42/run-9-1-differe/manifest.json",
	}}

	snapshot, err := LoadPreviewSnapshot(t.Context(), store, 42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StackStatus("api/prod").Succeeded {
		t.Fatal("matching preview was not selected")
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
	tests := []struct {
		name  string
		store *previewFailureStore
		want  error
	}{
		{name: "list", store: &previewFailureStore{Store: base, failList: true}, want: errPreviewListUnavailable},
		{name: "read", store: &previewFailureStore{Store: base, failGet: true}, want: errPreviewReadUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadPreviewSnapshot(t.Context(), tt.store, 42, sha); !errors.Is(err, tt.want) {
				t.Fatalf("LoadPreviewSnapshot error = %v, want %v", err, tt.want)
			}
		})
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
