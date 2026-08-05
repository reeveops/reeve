package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
)

func TestWriteOnce(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	w := NewWriter(store)

	e := Entry{
		RunID:      "run-1",
		Op:         "apply",
		StartedAt:  "2026-04-20T12:00:00Z",
		FinishedAt: "2026-04-20T12:02:00Z",
		Actor:      "alice",
		PR:         47,
		Repo:       "org/reeve",
		Outcome:    "success",
		DurationMS: 120_000,
		Stacks:     []Stack{{Ref: "api/prod", Env: "prod", Status: "ready", Add: 2}},
	}
	if err := w.Write(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Second write with same run-id should fail write-once.
	err := w.Write(ctx, e)
	if !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed, got %v", err)
	}

	// Read back and verify shape.
	data, _, err := filesystem.ReadBytes(ctx, store, "audit/2026/04/20/run-1.json")
	if err != nil {
		t.Fatal(err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version missing: %+v", got)
	}
	if got.Stacks[0].Add != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestBreakGlassEntryRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	w := NewWriter(store)

	e := Entry{
		RunID:      "apply-9-abc1234",
		Op:         "apply",
		StartedAt:  "2026-07-19T09:00:00Z",
		FinishedAt: "2026-07-19T09:01:00Z",
		Actor:      "alice",
		PR:         18,
		CommitSHA:  "abc1234def",
		Repo:       "org/repo",
		RunURL:     "https://ci.example/run/9",
		Outcome:    "success",
		BreakGlass: &BreakGlass{
			Justification:             "prod is down",
			AuthorizedVia:             "internal_list",
			OverriddenGates:           []string{"approvals", "not_in_freeze"},
			AuthorizingConfigModified: true,
			AuthorizingPathsModified:  []string{".reeve/shared.yaml"},
		},
	}
	if err := w.Write(ctx, e); err != nil {
		t.Fatal(err)
	}
	data, _, err := filesystem.ReadBytes(ctx, store, "audit/2026/07/19/apply-9-abc1234.json")
	if err != nil {
		t.Fatal(err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	bg := got.BreakGlass
	if bg == nil {
		t.Fatal("break_glass block missing")
	}
	if bg.Justification != "prod is down" || bg.AuthorizedVia != "internal_list" {
		t.Fatalf("round-trip mismatch: %+v", bg)
	}
	if len(bg.OverriddenGates) != 2 || !bg.AuthorizingConfigModified || bg.AuthorizingPathsModified[0] != ".reeve/shared.yaml" {
		t.Fatalf("round-trip mismatch: %+v", bg)
	}
	if got.RunURL != "https://ci.example/run/9" {
		t.Fatalf("run_url missing: %+v", got)
	}
}
