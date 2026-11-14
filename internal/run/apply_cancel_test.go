package run

import (
	"context"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
)

// blockingEngine's Apply blocks until the run context is cancelled -
// standing in for a `pulumi up` interrupted by SIGTERM.
type blockingEngine struct {
	bgEngine
	started chan struct{}
}

func (b *blockingEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	close(b.started)
	<-ctx.Done()
	return iac.ApplyResult{}, ctx.Err()
}

// TestApplyCancelledMidRunRecordsHoldsAndFails simulates a SIGTERM during
// an engine apply: the interrupted stack's apply returns the context error
// and its lock STAYS HELD recording why (outcome `interrupted`, run URL,
// audit key) - a lock held by an apply never lapses on its own - while the
// not-yet-started stack (its apply never began) is recorded as failed and
// its lock is released; the output demands a nonzero exit.
func TestApplyCancelledMidRunRecordsHoldsAndFails(t *testing.T) {
	engine := &blockingEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{
			{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
			{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
		}},
		started: make(chan struct{}),
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts", "projects/worker/main.ts"}, headSHA: bgSHA}
	store, _ := filesystem.New(t.TempDir())
	in := plainApplyInput(t, engine, fv, store)
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{
		Type: "pulumi",
		Stacks: []schemas.StackDecl{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
		},
	}}
	// A real preview run writes ONE manifest covering every stack it
	// previewed. Seed that shape: apply binds its scope to the newest
	// manifest for the commit, so a fixture that split two stacks across two
	// manifests would (correctly) apply only the stacks in the newer one.
	if err := writeManifest(context.Background(), store, 18, "preview-2", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-engine.started // first stack is mid-apply
		cancel()         // SIGTERM arrives
	}()

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Failed {
		t.Fatalf("cancelled run must be failed: %+v", out)
	}
	if len(out.FailedStacks) != 2 {
		t.Fatalf("both stacks must be failed (mid-apply + never-started): %v", out.FailedStacks)
	}

	// The mid-apply stack carries the context error; the second one the
	// cancelled-before marker.
	byRef := map[string]summary.StackSummary{}
	for _, s := range out.Stacks {
		byRef[s.Ref()] = s
	}
	if s := byRef["worker/prod"]; !strings.Contains(s.Error, "cancelled before") {
		t.Fatalf("never-started stack must say cancelled-before: %+v", s)
	}

	// worker/prod's apply never began: its lock (never persistently held)
	// is released.
	worker, _, gerr := in.Locks.Get(context.Background(), "worker", "prod")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if worker.Holder != nil {
		t.Fatalf("the never-started stack's lock must be free: %+v", worker.Holder)
	}

	// api/prod's apply was INTERRUPTED: its lock stays held with the reason.
	api, _, gerr := in.Locks.Get(context.Background(), "api", "prod")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if api.Holder == nil || !api.Holder.HoldStarted {
		t.Fatalf("the interrupted stack's lock must stay held: %+v", api.Holder)
	}
	if api.Holder.Outcome != "interrupted" {
		t.Fatalf("hold outcome = %q, want interrupted: %+v", api.Holder.Outcome, api.Holder)
	}
	if api.Holder.AuditKey == "" || api.Holder.RecordedAt == "" {
		t.Fatalf("the interrupted hold must carry the audit key and timestamp: %+v", api.Holder)
	}

	// The interrupted failure notice reaches the PR.
	if !strings.Contains(fv.allComments(), "STILL HELD") {
		t.Fatalf("the PR must carry the held-lock notice: %s", fv.allComments())
	}
}
