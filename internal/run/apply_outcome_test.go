package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// failEngine is a fake applyEngine whose Apply fails for selected refs.
type failEngine struct {
	bgEngine
	failRefs map[string]string // ref → error message
}

func (f *failEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	f.applied = append(f.applied, s.Ref())
	if msg, ok := f.failRefs[s.Ref()]; ok {
		return iac.ApplyResult{}, errors.New(msg)
	}
	return iac.ApplyResult{Counts: summary.Counts{Add: 1}}, nil
}

// plainShared builds a non-break-glass shared config whose approvals gate is
// satisfiable by one review (the fixture VCS supplies it).
func plainShared() *schemas.Shared {
	one := 1
	return &schemas.Shared{
		Bucket: schemas.BucketConfig{Type: "filesystem"},
		Approvals: schemas.ApprovalsYAML{
			Default: schemas.ApprovalRuleYAML{RequiredApprovals: &one},
		},
	}
}

// plainApplyInput wires a gates-green, non-break-glass apply fixture.
func plainApplyInput(t *testing.T, engine applyEngine, fv *bgVCS, store blob.Store) ApplyInput {
	t.Helper()
	in := bgApplyInput(t, &bgEngine{}, fv, plainShared(), store)
	in.Engine = engine
	in.BreakGlass = nil
	fv.repoPrivate = true // one unlisted review only gates a private repo
	fv.approvalsList = []approvals.Approval{
		{Source: "pr_review", Approver: "reviewer", CommitSHA: bgSHA, Pinned: true},
	}
	return in
}

func TestApplyFailedStackSetsFailedOutput(t *testing.T) {
	ctx := context.Background()
	engine := &failEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
		failRefs: map[string]string{"api/prod": "pulumi up exploded"},
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	store, _ := filesystem.New(t.TempDir())
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Failed {
		t.Fatalf("engine failure must set Failed: %+v", out)
	}
	if len(out.FailedStacks) != 1 || out.FailedStacks[0] != "api/prod" {
		t.Fatalf("FailedStacks = %v, want [api/prod]", out.FailedStacks)
	}
	if out.Blocked {
		t.Fatalf("failure is not blocked: %+v", out)
	}

	// Lock must have been released after the failed stack.
	l, _, err := in.Locks.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Fatalf("lock must be released after engine failure: %+v", l.Holder)
	}

	// Audit records the failed outcome.
	e := readAuditEntry(t, store)
	if e.Outcome != "failed" {
		t.Fatalf("audit outcome = %q, want failed", e.Outcome)
	}
}

func TestApplyCleanRunNotFailed(t *testing.T) {
	ctx := context.Background()
	engine := &failEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	store, _ := filesystem.New(t.TempDir())
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Failed || out.Blocked || len(out.FailedStacks) != 0 {
		t.Fatalf("clean run must not be failed/blocked: %+v", out)
	}
	e := readAuditEntry(t, store)
	if e.Outcome != "success" {
		t.Fatalf("audit outcome = %q, want success", e.Outcome)
	}
}

// TestApplySamePRActiveHolderBlockedWithExpiry: an actively acquired holder
// from another run of the same PR blocks this run (not queued, not applied)
// and the refusal names the holder run and its lease expiry.
func TestApplySamePRActiveHolderBlockedWithExpiry(t *testing.T) {
	ctx := context.Background()
	engine := &failEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	store, _ := filesystem.New(t.TempDir())
	in := plainApplyInput(t, engine, fv, store)

	// Another live run of the same PR holds the stack lock.
	seeded, ok, err := in.Locks.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 18, RunID: "other-run"}, time.Hour)
	if err != nil || !ok {
		t.Fatalf("seed: ok=%v err=%v", ok, err)
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked || out.Failed {
		t.Fatalf("same-PR active holder must block, not fail: %+v", out)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("engine must not run: %v", engine.applied)
	}
	var msg string
	for _, s := range out.Stacks {
		if s.Ref() == "api/prod" {
			msg = s.Error
		}
	}
	for _, want := range []string{"other-run", seeded.Holder.ExpiresAt} {
		if !strings.Contains(msg, want) {
			t.Fatalf("blocked message %q missing %q", msg, want)
		}
	}
}
