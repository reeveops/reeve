package run

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	corelocks "github.com/reeveops/reeve/internal/core/locks"

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
		"held by PR #18 (hold started",
		"recorded outcome \"failed\"",
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

// countingCASStore fails the Nth locks/ PutIfMatch call (1-based; the
// acquire path uses two: a conditional-write probe create, then the
// conditional write) and passes everything else: acquire is call 1, MarkHeld call
// 2, RecordOutcome the calls after - so tests can point the failure at one
// step of the hold lifecycle.
type countingCASStore struct {
	blob.Store
	failOnN     int
	failAtLeast bool // fail every call from failOnN on, instead of exactly one
	n           int
}

func (c *countingCASStore) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	// Only lock-file CAS calls count: other blob writes (timelines,
	// manifests) share the store and must not shift the schedule.
	if strings.HasPrefix(key, "locks/") {
		c.n++
		if c.n == c.failOnN || (c.failAtLeast && c.n >= c.failOnN) {
			return nil, errors.New("simulated CAS outage")
		}
	}
	return c.Store.PutIfMatch(ctx, key, r, ifMatch)
}

// TestMarkHeldFailurePreventsEngine pins the mandatory hold: if the hold
// cannot be marked persistent, the engine does NOT start - nothing has been
// applied. The stack errors naming why, and the acquired lock is released.
func TestMarkHeldFailurePreventsEngine(t *testing.T) {
	ctx := context.Background()
	inner, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	store := &countingCASStore{Store: inner, failOnN: 2} // acquire ok, MarkHeld fails, release ok
	in := plainApplyInput(t, engine, fv, store)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Failed {
		t.Fatalf("the refused stack must fail the run: %+v", out)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("the engine must not run when the hold cannot be marked: %v", engine.applied)
	}
	var ss summary.StackSummary
	for _, s := range out.Stacks {
		if s.Ref() == "api/prod" {
			ss = s
		}
	}
	if ss.Status != summary.StatusError {
		t.Fatalf("the stack must error, got %q: %s", ss.Status, ss.Error)
	}
	for _, want := range []string{"could not be marked persistent", "Nothing was applied"} {
		if !strings.Contains(ss.Error+"", want) {
			t.Fatalf("the refusal must say what happened and that nothing ran:\nwant: %s\ngot:  %q", want, ss.Error)
		}
	}

	// The acquired lock is released: the holder is gone.
	l, _, gerr := blocks.New(inner).Get(ctx, "api", "prod")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if l.Holder != nil {
		t.Fatalf("the lock must be released after the refused hold: %+v", l.Holder)
	}
}

// TestRecordOutcomeFailureLogsNoReason pins the retry + final log: when the
// outcome record cannot be written even after retries, the run logs that the
// lock REMAINS HELD with no recorded reason - not the false "keeps its lease
// semantics" - and names the run URL.
func TestRecordOutcomeFailureLogsNoReason(t *testing.T) {
	var captured []string
	h := slog.NewTextHandler(&capturingWriter{lines: &captured}, &slog.HandlerOptions{Level: slog.LevelInfo})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := context.Background()
	inner, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	store := &countingCASStore{Store: inner, failOnN: 3, failAtLeast: true} // acquire + MarkHeld ok, RecordOutcome retries fail
	in := plainApplyInput(t, &failEngine{bgEngine: bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}},
		failRefs: map[string]string{"api/prod": "explode"}}, fv, store)
	in.CIRunURL = "https://ci.example/run/13"

	out, err := Apply(ctx, in)
	if err != nil || !out.Failed {
		t.Fatalf("the run must fail: out=%+v err=%v", out, err)
	}
	joined := strings.Join(captured, "\n")
	for _, want := range []string{
		"REMAINS HELD with no recorded reason",
		"https://ci.example/run/13",
		"simulated CAS outage",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the final outcome-record failure log must state the truth:\nwant: %s\ngot:\n%s", want, joined)
		}
	}
	// The hold itself persists (never lapsed), with no recorded reason.
	l, _, gerr := blocks.New(inner).Get(ctx, "api", "prod")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if l.Holder == nil || !l.Holder.HoldStarted || l.Holder.Outcome != "" {
		t.Fatalf("the hold must persist unrecorded: %+v", l.Holder)
	}
}

// TestSamePRRetryRefusalShowsReasonAndCommand: a same-PR retry of a run that
// recorded a failed outcome is blocked with the recorded reason (outcome, run
// URL, audit key) and the exact command that clears the hold.
func TestSamePRRetryRefusalShowsReasonAndCommand(t *testing.T) {
	ctx := context.Background()
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

	fv2 := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in2 := plainApplyInput(t, &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}, fv2, blobStore)
	in2.RunNumber = 8
	in2.CIRunURL = "https://ci.example/run/8"
	out2, err := Apply(ctx, in2)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if !out2.Blocked {
		t.Fatalf("the same-PR retry must be blocked by its own hold: %+v", out2)
	}
	var ss summary.StackSummary
	for _, s := range out2.Stacks {
		if s.Ref() == "api/prod" {
			ss = s
		}
	}
	for _, want := range []string{
		"another run of PR #18",
		`recorded outcome "failed"`,
		"https://ci.example/run/7",
		"audit/2026/",
		"/reeve unlock api/prod --force",
	} {
		if !strings.Contains(ss.Error, want) {
			t.Fatalf("the same-PR refusal must name the reason and the clearing command:\nwant: %s\ngot:  %s", want, ss.Error)
		}
	}
	// The PR-comment hint must be the comment-parser form: no --pr flag.
	if strings.Contains(ss.Error, "--pr") {
		t.Fatalf("the PR-comment hint must not carry the CLI-only --pr flag: %s", ss.Error)
	}
}

// TestHoldBlockedMessageShowsRunDied pins that a runner-killed hold (hold
// started, lease expired, no outcome) renders the run-died text in the other
// run's lock-gate refusal.
func TestHoldBlockedMessageShowsRunDied(t *testing.T) {
	blobStore, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	s := blocks.New(blobStore)
	ctx := context.Background()
	if _, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 18, RunID: "apply-1", Actor: "alice"}, -time.Hour); err != nil || !ok {
		t.Fatalf("seed holder: ok=%v err=%v", ok, err)
	}
	if err := s.MarkHeld(ctx, "api", "prod", corelocks.Holder{PR: 18, RunID: "apply-1"}); err != nil {
		t.Fatal(err)
	}
	// The lease is already in the past and no outcome was recorded: the
	// runner most likely died.
	l, _, err := s.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	l.Holder.ExpiresAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}, fv, blobStore)
	in.PRNumber = 19
	in.Locks = s
	if err := writeManifest(context.Background(), blobStore, 19, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("the second PR must be blocked by the run-died hold: %+v", out)
	}
	comments := fv.allComments()
	if !strings.Contains(comments, "most likely died") {
		t.Fatalf("the blocked message must say the run most likely died:\n%s", comments)
	}
}

// TestHoldStatusLiveVsExpired pins the hold-state wording: an expired
// started hold with no outcome says the run most likely died; a LIVE
// started hold with no outcome says the apply is in progress (and when the
// lease expires) - and never guesses a death while the lease lives. A hold
// with a recorded outcome keeps its outcome text either way.
func TestHoldStatusLiveVsExpired(t *testing.T) {
	now := time.Now()
	live := &corelocks.Holder{PR: 7, RunID: "r1", HoldStarted: true,
		ExpiresAt: now.Add(time.Hour).UTC().Format(time.RFC3339)}
	got := live.HoldStatus(now)
	for _, want := range []string{"hold started", "lease expires", "apply is in progress"} {
		if !strings.Contains(got, want) {
			t.Fatalf("a live started hold must read in progress (with the lease expiry):\nwant: %s\ngot:  %s", want, got)
		}
	}
	if strings.Contains(got, "most likely died") {
		t.Fatalf("a live lease must not be worded as a run death: %s", got)
	}

	expired := &corelocks.Holder{PR: 7, RunID: "r1", HoldStarted: true,
		ExpiresAt: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)}
	got = expired.HoldStatus(now)
	for _, want := range []string{"lease expired", "no outcome recorded", "most likely died", "validate the stack's real state before unlocking"} {
		if !strings.Contains(got, want) {
			t.Fatalf("an expired started hold must read as a run death:\nwant: %s\ngot:  %s", want, got)
		}
	}

	recorded := &corelocks.Holder{PR: 7, RunID: "r1", HoldStarted: true,
		ExpiresAt: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		Outcome:   "failed", RecordedAt: "2026-11-12T00:00:00Z"}
	got = recorded.HoldStatus(now)
	if !strings.Contains(got, `recorded outcome "failed"`) || strings.Contains(got, "most likely died") {
		t.Fatalf("a recorded outcome must own the wording: %s", got)
	}
}
