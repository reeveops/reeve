package run

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/summary"
)

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
