package run

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
)

// gateLeaseEngine makes the first apply outlive the lease taken for the
// second stack during the gate phase. Another PR then tries to take that lock
// before the second stack's engine call.
type gateLeaseEngine struct {
	bgEngine
	locks              *blocks.Store
	ttl                time.Duration
	competitorAcquired bool
	firstApplyElapsed  time.Duration
	originalExpiry     time.Time
	secondExpiry       time.Time
}

func (e *gateLeaseEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	e.applied = append(e.applied, s.Ref())
	switch s.Ref() {
	case "api/prod":
		l, _, err := e.locks.Get(ctx, "worker", "prod")
		if err != nil {
			return iac.ApplyResult{}, err
		}
		if l.Holder == nil || l.Holder.PR != 18 {
			return iac.ApplyResult{}, fmt.Errorf("worker lock not held by applying PR at first apply: %+v", l.Holder)
		}
		e.originalExpiry, err = time.Parse(time.RFC3339, l.Holder.ExpiresAt)
		if err != nil {
			return iac.ApplyResult{}, err
		}

		started := time.Now()
		time.Sleep(e.ttl + 600*time.Millisecond)
		e.firstApplyElapsed = time.Since(started)
		_, acquired, err := e.locks.TryAcquire(ctx, "worker", "prod", corelocks.Holder{
			PR: 999, RunID: "competitor",
		}, e.ttl)
		if err != nil {
			return iac.ApplyResult{}, fmt.Errorf("competitor acquire: %w", err)
		}
		e.competitorAcquired = acquired
	case "worker/prod":
		l, _, err := e.locks.Get(ctx, "worker", "prod")
		if err != nil {
			return iac.ApplyResult{}, err
		}
		if l.Holder == nil || l.Holder.PR != 18 || l.Holder.RunID == "competitor" {
			return iac.ApplyResult{}, fmt.Errorf("worker lock lost before its apply started: %+v", l.Holder)
		}
		e.secondExpiry, err = time.Parse(time.RFC3339, l.Holder.ExpiresAt)
		if err != nil {
			return iac.ApplyResult{}, err
		}
	}
	return iac.ApplyResult{Counts: summary.Counts{Add: 1}}, nil
}

func TestApplyHeartbeatsLaterStackLockFromGateAcquisition(t *testing.T) {
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const ttl = time.Second
	engine := &gateLeaseEngine{
		bgEngine: bgEngine{enum: []discovery.Stack{
			{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
			{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
		}},
		ttl: ttl,
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts", "projects/worker/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)
	engine.locks = in.Locks
	in.Shared.Locking.TTL = ttl.String()
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{
		Type: "pulumi",
		Stacks: []schemas.StackDecl{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
		},
	}}
	if err := writeManifest(context.Background(), store, 18, "preview-2", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Blocked || out.Failed {
		t.Fatalf("both stacks must apply: %+v", out)
	}
	if len(engine.applied) != 2 {
		t.Fatalf("applied stacks = %v, want both", engine.applied)
	}
	if engine.firstApplyElapsed <= ttl {
		t.Fatalf("first apply lasted %s, want longer than the %s lease", engine.firstApplyElapsed, ttl)
	}
	if engine.competitorAcquired {
		t.Fatal("another PR acquired the later stack's lock before its apply started")
	}
	if !engine.secondExpiry.After(engine.originalExpiry) {
		t.Fatalf("later stack lease was not refreshed: original=%s at apply=%s", engine.originalExpiry, engine.secondExpiry)
	}
}
