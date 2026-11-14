package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
)

// lostLockEngine stands in for `pulumi up` whose stack lock is taken away
// mid-run: it signals started, clears the lock exactly as an operator
// force-unlock would, then blocks until its context is cancelled - the
// SIGINT + grace path an interrupted engine gets.
type lostLockEngine struct {
	bgEngine
	started chan struct{}
	strip   func()
}

func (b *lostLockEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	close(b.started)
	b.strip()
	select {
	case <-ctx.Done():
		return iac.ApplyResult{}, ctx.Err()
	case <-time.After(30 * time.Second):
		return iac.ApplyResult{}, errors.New("engine was not aborted after the lock was lost")
	}
}

// TestApplyAbortedOnLockLoss drives the whole fix: an apply whose lease is
// force-unlocked mid-flight must cancel the engine instead of running on
// unheld, fail the stack with the observed state and the next step, and
// never re-take the lock. The old heartbeat refreshed via TryAcquire, which
// reinstalled the holder after a force-unlock and let the apply finish on a
// lock it no longer owned.
func TestApplyAbortedOnLockLoss(t *testing.T) {
	ctx := context.Background()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &lostLockEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
		started:  make(chan struct{}),
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)
	// LockTTL defaults to 4h; a short ttl makes the heartbeat tick within
	// the test so the loss is detected while the engine is mid-apply.
	in.Shared.Locking.TTL = "1200ms"
	engine.strip = func() {
		if _, err := in.Locks.ForceUnlock(context.Background(), "api", "prod", 0); err != nil {
			t.Errorf("force-unlock mid-apply: %v", err)
		}
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Failed {
		t.Fatalf("lock loss must fail the run: %+v", out)
	}
	if len(out.FailedStacks) != 1 || out.FailedStacks[0] != "api/prod" {
		t.Fatalf("FailedStacks = %v, want [api/prod]", out.FailedStacks)
	}

	var ss summary.StackSummary
	for _, s := range out.Stacks {
		if s.Ref() == "api/prod" {
			ss = s
		}
	}
	for _, want := range []string{
		"Apply aborted on api/prod: lock lost at ",
		"(released; the lock has no holder)",
		"Engine interrupted; resources may be partially applied.",
		"Run `/reeve preview` to see the current state before re-applying.",
	} {
		if !strings.Contains(ss.Error, want) {
			t.Errorf("stack error must carry the refusal:\n want: %s\ngot:  %s", want, ss.Error)
		}
	}
	if ss.Status != summary.StatusError {
		t.Fatalf("status = %q, want error", ss.Status)
	}

	// The lock is not retaken: no holder, no queue entry left behind.
	l, _, err := in.Locks.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil || len(l.Queue) != 0 {
		t.Fatalf("lock must not be held after the abort: holder=%+v queue=%+v", l.Holder, l.Queue)
	}
}
