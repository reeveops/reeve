package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	best := newestPreviewManifest(ctx, store, 42, sha)
	if best == nil {
		t.Fatal("expected a manifest")
	}
	if best.RunID != "run-2" {
		t.Fatalf("tie-break should pick the higher run id, got %q", best.RunID)
	}

	// The authoritative manifest has a failed stack, so this must be false.
	// If it reports true, the two selections disagree.
	if PlanSucceededForPR(ctx, store, 42, sha) {
		t.Fatal("PlanSucceededForPR reported success from a manifest other than the authoritative one")
	}
}
