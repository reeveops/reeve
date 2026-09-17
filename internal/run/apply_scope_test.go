package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
)

// twoStackInput builds an apply over two declared stacks whose changed files
// include one that maps to no stack - the input that makes change mapping
// broaden to everything.
func twoStackInput(t *testing.T, engine *bgEngine, store *filesystem.Store) ApplyInput {
	t.Helper()
	engine.enum = []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
	}
	fv := &bgVCS{
		changed: []string{
			"projects/api/main.ts", // maps to api/prod
			"shared/versions.ts",   // maps to nothing -> broadens
		},
		headSHA: bgSHA,
	}
	in := plainApplyInput(t, engine, fv, store)
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{
		Type: "pulumi",
		Stacks: []schemas.StackDecl{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
		},
	}}
	return in
}

// The reported production bug: a PR that PLANNED one stack applied every
// stack, because one changed file mapped to nothing and change mapping
// broadens to the full set. Apply must execute the previewed set, not a
// mapping recomputed at apply time.
func TestApplyDoesNotBroadenBeyondThePlan(t *testing.T) {
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)

	// plainApplyInput seeds a manifest covering api/prod only - i.e. the
	// preview mapped to one stack. worker/prod was never planned.
	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
		t.Fatalf("apply must touch only the previewed stack, got %v", engine.applied)
	}
	for _, s := range out.Stacks {
		if s.Ref() == "worker/prod" {
			t.Errorf("worker/prod was never previewed and must not appear in the apply result")
		}
	}
}

// When the plan DID cover both stacks, both apply - narrowing must come from
// the plan, not from being conservative for its own sake.
func TestApplyAppliesEveryPreviewedStack(t *testing.T) {
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)

	// A later preview covering both stacks supersedes the seeded one.
	if err := writeManifest(context.Background(), store, 18, "preview-2", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(context.Background(), in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.applied) != 2 {
		t.Fatalf("both previewed stacks must apply, got %v", engine.applied)
	}
}

func TestApplyUsesPreviewWhenLiveFilesMoveOutsideRoot(t *testing.T) {
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)
	in.RepoPath = "infra"
	in.VCS.(*bgVCS).changed = []string{"docs/readme.md"}

	if _, err := Apply(context.Background(), in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
		t.Fatalf("apply must retain the previewed stack when the live diff moves outside the root, got %v", engine.applied)
	}
}

func TestApplyRevalidatesExpectedHeadBeforeStateChange(t *testing.T) {
	expected := strings.Repeat("a", 40)
	moved := strings.Repeat("b", 40)
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)
	fv := in.VCS.(*bgVCS)
	fv.headSHAs = []string{expected, moved}
	in.CommitSHA = expected
	in.ExpectedHeadSHA = expected
	fv.approvalsList = []approvals.Approval{{Source: "pr_review", Approver: "reviewer", CommitSHA: expected, Pinned: true}}
	if err := writeManifest(context.Background(), store, in.PRNumber, "expected-preview", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, expected); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(context.Background(), in)
	if !errors.Is(err, ErrPRHeadMismatch) {
		t.Fatalf("Apply error = %v, want %v", err, ErrPRHeadMismatch)
	}
	if fv.getPRCalls != 2 {
		t.Fatalf("PR metadata reads = %d, want initial snapshot plus boundary revalidation", fv.getPRCalls)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("engine applied after PR head moved: %v", engine.applied)
	}
}

// With no plan for the commit at all, apply must not fall back to "every
// stack". It stops and says to preview first.
func TestApplyWithNoPlanForCommitDoesNotWiden(t *testing.T) {
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)
	in.CommitSHA = "commit-with-no-preview"
	in.VCS.(*bgVCS).headSHA = in.CommitSHA

	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("no plan exists for this commit; nothing may apply, got %v", engine.applied)
	}
	if out.Failed {
		t.Errorf("a missing plan is a skip, not a failure: %+v", out)
	}
}

func TestApplyFailsClosedOnMalformedPreviewHistory(t *testing.T) {
	t.Parallel()
	engine := &bgEngine{}
	store, _ := filesystem.New(t.TempDir())
	in := twoStackInput(t, engine, store)
	putRawManifest(t, store, in.PRNumber, "corrupt", in.CommitSHA, "{not-json")

	_, err := Apply(t.Context(), in)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("Apply error = %v, want *json.SyntaxError", err)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("malformed preview history allowed applies: %v", engine.applied)
	}
}

// scopeDelta names what a recomputed mapping would have added, so the drop is
// visible on the timeline rather than silent.
func TestScopeDeltaNamesDroppedStacks(t *testing.T) {
	mapped := []discovery.Stack{
		{Project: "api", Name: "prod"},
		{Project: "worker", Name: "prod"},
		{Project: "edge", Name: "prod"},
	}
	bound := []discovery.Stack{{Project: "api", Name: "prod"}}

	got := strings.Join(scopeDelta(mapped, bound), ",")
	if got != "edge/prod,worker/prod" {
		t.Errorf("scopeDelta = %q, want the sorted non-previewed refs", got)
	}
}
