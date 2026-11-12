package run

import (
	"context"
	"strings"
	"testing"

	"log/slog"

	"github.com/reeveops/reeve/internal/blob/filesystem"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
)

// TestHeldLockNeverFreeAndRefusalCarriesReason pins the maintainer decision:
// a lock whose apply STARTED never lapses on its own - not by expiry, not by
// heartbeat failure. A second PR is queued behind it and its lock-gate
// refusal carries the hold reason (holder PR, recorded outcome, run URL,
// audit key).
func TestHeldLockNeverFreeAndRefusalCarriesReason(t *testing.T) {
	ctx := context.Background()

	// The first run's apply fails, leaving the lock held with its reason.
	blobStore, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	fv1 := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in1 := plainApplyInput(t, &failEngine{bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
		failRefs: map[string]string{"api/prod": "pulumi up exploded"}}, fv1, blobStore)
	in1.CIRunURL = "https://ci.example/run/7"
	if out, err := Apply(ctx, in1); err != nil || !out.Failed {
		t.Fatalf("first run must fail: out=%+v err=%v", out, err)
	}

	l, _, err := in1.Locks.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.Outcome != "failed" || !l.Holder.HoldStarted {
		t.Fatalf("the failed apply's lock must stay held with outcome: %+v", l.Holder)
	}

	// The second run: a DIFFERENT PR against the held lock. (A same-PR
	// re-run would hit the idempotent already-holder path.)
	fv2 := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in2 := plainApplyInput(t, &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}, fv2, blobStore)
	in2.Locks = in1.Locks
	in2.CIRunURL = "https://ci.example/run/8"
	in2.PRNumber = 19
	if err := writeManifest(context.Background(), blobStore, 19, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	out2, err := Apply(ctx, in2)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if !out2.Blocked {
		t.Fatalf("the second run must be blocked by the held lock: %+v", out2)
	}
	var ss summary.StackSummary
	for _, s := range out2.Stacks {
		if s.Ref() == "api/prod" {
			ss = s
		}
	}
	lockGate := ""
	for _, g := range ss.Gates {
		if g.Gate == "lock_acquirable" {
			lockGate = g.Reason
		}
	}
	for _, want := range []string{
		"held by PR #18 (outcome \"failed\"",
		"https://ci.example/run/7",
		"audit/2026/",
		"the stack's real state must be validated",
	} {
		if !strings.Contains(lockGate, want) {
			t.Fatalf("the lock-gate refusal must carry the hold reason:\nwant: %s\ngate: %s", want, lockGate)
		}
	}

	// The held lock survives the blocked run.
	l, _, _ = in1.Locks.Get(ctx, "api", "prod")
	if l.Holder == nil || l.Holder.Outcome != "failed" {
		t.Fatalf("the blocked run must not free the hold: %+v", l.Holder)
	}
}

// TestPartialFailureRecordedAndCommented pins: one stack succeeds, one
// fails -> the failed stack's hold records "partial"; the PR carries the
// held-lock notice; the audit key is present.
func TestPartialFailureRecordedAndCommented(t *testing.T) {
	ctx := context.Background()
	fv := &bgVCS{changed: []string{"projects/api/main.ts", "projects/worker/main.ts"}, headSHA: bgSHA}
	blobStore, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	engine := &failEngine{bgEngine: bgEngine{enum: []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
	}},
		failRefs: map[string]string{"worker/prod": "terraform apply exploded"},
	}
	in := plainApplyInput(t, engine, fv, blobStore)
	in.CIRunURL = "https://ci.example/run/9"
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{
		Type: "pulumi",
		Stacks: []schemas.StackDecl{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
		},
	}}
	if err := writeManifest(context.Background(), blobStore, 18, "preview-2", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Failed {
		t.Fatalf("the failed stack must fail the run: %+v", out)
	}

	l, _, gerr := in.Locks.Get(ctx, "worker", "prod")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if l.Holder == nil || l.Holder.Outcome != "partial" {
		t.Fatalf("the failing stack after a passing stack must record partial: %+v", l.Holder)
	}
	if l.Holder.RunURL == "" || l.Holder.AuditKey == "" || l.Holder.RecordedAt == "" {
		t.Fatalf("the hold must carry the pointers: %+v", l.Holder)
	}

	// The PR notice names the failed stack, the hold, and the pointers.
	notice := fv.allComments()
	for _, want := range []string{"STILL HELD", "worker/prod", "https://ci.example/run/9", "audit/2026/"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("the held-lock notice must carry the failure and pointers:\nwant: %s\nnotice:\n%s", want, notice)
		}
	}
}

// TestFailedApplyLogsHoldAtErrorLevel pins that the hold notice from a
// failed apply is logged at error level, carrying the stack, outcome,
// pointers, and the failure.
func TestFailedApplyLogsHoldAtErrorLevel(t *testing.T) {
	var captured []string
	capture := slog.NewTextHandler(&capturingWriter{lines: &captured}, &slog.HandlerOptions{Level: slog.LevelInfo})
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := context.Background()
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	blobStore, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	in := plainApplyInput(t, &failEngine{bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
		failRefs: map[string]string{"api/prod": "explode"}}, fv, blobStore)
	in.CIRunURL = "https://ci.example/run/11"

	if out, err := Apply(ctx, in); err != nil || !out.Failed {
		t.Fatalf("the run must fail: out=%+v err=%v", out, err)
	}
	joined := strings.Join(captured, "\n")
	for _, want := range []string{
		"level=ERROR",
		"the lock stays held",
		"api/prod",
		"outcome=failed",
		"https://ci.example/run/11",
		"audit/2026/",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the error-level hold log must carry:\nwant: %s\ngot:\n%s", want, joined)
		}
	}
}

type capturingWriter struct {
	lines *[]string
}

func (w *capturingWriter) Write(p []byte) (int, error) {
	*w.lines = append(*w.lines, string(p))
	return len(p), nil
}
