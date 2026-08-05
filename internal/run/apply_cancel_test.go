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

// TestApplyCancelledMidRunReleasesLocksAndFails simulates a SIGTERM during
// an engine apply: the blocked subprocess returns the context error, the
// stack lock is released on a detached grace context (not left pinned for
// the ttl), the not-yet-started stack is recorded as failed, and the output
// demands a nonzero exit.
func TestApplyCancelledMidRunReleasesLocksAndFails(t *testing.T) {
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

	// Both locks must be free: api/prod released on the detached grace
	// context after the engine returned; worker/prod never acquired.
	for _, ref := range [][2]string{{"api", "prod"}, {"worker", "prod"}} {
		l, _, gerr := in.Locks.Get(context.Background(), ref[0], ref[1])
		if gerr != nil {
			t.Fatal(gerr)
		}
		if l.Holder != nil {
			t.Fatalf("lock %s/%s must not be left held after cancellation: %+v", ref[0], ref[1], l.Holder)
		}
	}
}
