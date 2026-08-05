package drift

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// fakeEngine returns a canned DriftCheck result.
type fakeEngine struct {
	res iac.PreviewResult
	err error
}

func (fakeEngine) Name() string { return "fake" }
func (fakeEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return nil, nil
}
func (f fakeEngine) DriftCheck(context.Context, discovery.Stack, iac.PreviewOpts, bool) (iac.PreviewResult, error) {
	return f.res, f.err
}

func runOneWith(engine Engine, mode string) (Item, Event) {
	opts := Options{
		Engine:        engine,
		Redactor:      redact.New(),
		BootstrapMode: mode,
		// nil StateStore/SuppressionStore/AuthResolver: runOne handles nil.
	}
	item, ev, _, _ := runOne(context.Background(), opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	return item, ev
}

// TestRunOneFailedCheckIsNotNoDrift is the regression for the critical
// false-resolve: a DriftCheck that fails (non-nil error, empty result) must
// classify as an error / check_failed, never as no_drift - otherwise an
// active drift alert silently resolves.
func TestRunOneFailedCheckIsNotNoDrift(t *testing.T) {
	// Empty result + non-nil error (e.g. timeout kill with no stderr).
	item, ev := runOneWith(fakeEngine{res: iac.PreviewResult{}, err: errors.New("context deadline exceeded")}, "")
	if item.Outcome != OutcomeError {
		t.Fatalf("failed check must be OutcomeError, got %s", item.Outcome)
	}
	if ev != EventCheckFailed {
		t.Fatalf("failed check must emit check_failed, got %s", ev)
	}

	// Error string set but no error return (refresh-style failure path).
	item, ev = runOneWith(fakeEngine{res: iac.PreviewResult{Error: "refresh failed"}}, "")
	if item.Outcome != OutcomeError || ev != EventCheckFailed {
		t.Fatalf("error-string result must be a failed check, got outcome=%s ev=%s", item.Outcome, ev)
	}
}

func TestRunOneNoDriftAndDrift(t *testing.T) {
	// Genuinely clean: empty counts, no error → no_drift, silent.
	item, ev := runOneWith(fakeEngine{res: iac.PreviewResult{}}, "")
	if item.Outcome != OutcomeNoDrift || ev != EventNone {
		t.Fatalf("clean check must be silent no_drift, got outcome=%s ev=%s", item.Outcome, ev)
	}

	// Drift with changed resources → drift_detected, fingerprint from URNs.
	res := iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn:pulumi:prod::app::aws:s3/bucket:Bucket::data"},
	}
	item, ev = runOneWith(fakeEngine{res: res}, "")
	if item.Outcome != OutcomeDriftDetected || ev != EventDriftDetected {
		t.Fatalf("changed resources must detect drift, got outcome=%s ev=%s", item.Outcome, ev)
	}
	if item.Fingerprint == "" {
		t.Fatal("drift fingerprint should be set from DriftedURNs")
	}
}

// TestRunOneCorruptStateFailsLoudly: a state file that cannot be read must
// fail the stack's check (run_error → exit_on machinery), never silently
// degrade to first-run semantics.
func TestRunOneCorruptStateFailsLoudly(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(ctx, "drift/state/p/s.json", strings.NewReader("{corrupt")); err != nil {
		t.Fatal(err)
	}
	opts := Options{Engine: fakeEngine{}, Redactor: redact.New(), StateStore: &StateStore{Blob: fs}}
	item, ev, skip, _ := runOne(ctx, opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	if skip {
		t.Fatal("a state-load failure must not be skipped")
	}
	if item.Outcome != OutcomeError || ev != EventCheckFailed {
		t.Fatalf("want error/check_failed, got outcome=%s ev=%s", item.Outcome, ev)
	}
	if !strings.Contains(item.Error, "load drift state") {
		t.Fatalf("error should name the state load, got %q", item.Error)
	}
}

// TestRunOneCorruptSuppressionFailsLoudly: an unreadable suppression must
// not be treated as "not suppressed".
func TestRunOneCorruptSuppressionFailsLoudly(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(ctx, "drift/suppressions/p/s.json", strings.NewReader("{corrupt")); err != nil {
		t.Fatal(err)
	}
	opts := Options{Engine: fakeEngine{}, Redactor: redact.New(), SuppressionStore: &SuppressionStore{Blob: fs}}
	item, ev, _, _ := runOne(ctx, opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	if item.Outcome != OutcomeError || ev != EventCheckFailed {
		t.Fatalf("want error/check_failed, got outcome=%s ev=%s", item.Outcome, ev)
	}
	if !strings.Contains(item.Error, "suppression") {
		t.Fatalf("error should name the suppression load, got %q", item.Error)
	}
}

// TestDriftEnvCounts: every env seen this run gets an entry, including an
// explicit zero for recovered/clean envs (the OTEL gauge reset).
func TestDriftEnvCounts(t *testing.T) {
	items := []Item{
		{Env: "prod", Outcome: OutcomeDriftDetected},
		{Env: "prod", Outcome: OutcomeDriftDetected},
		{Env: "staging", Outcome: OutcomeNoDrift},
		{Env: "dev", Outcome: OutcomeError},
	}
	got := driftEnvCounts(items)
	want := map[string]int64{"prod": 2, "staging": 0, "dev": 0}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for env, n := range want {
		if v, ok := got[env]; !ok || v != n {
			t.Fatalf("env %s: got %v, want %d (zero entries must be present)", env, got, n)
		}
	}
}

// slowEngine blocks in DriftCheck until either delay elapses or ctx is
// cancelled, mimicking a hung engine invocation that honors cancellation.
type slowEngine struct {
	delay time.Duration
	res   iac.PreviewResult
}

func (slowEngine) Name() string { return "slow" }
func (slowEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return nil, nil
}
func (e slowEngine) DriftCheck(ctx context.Context, _ discovery.Stack, _ iac.PreviewOpts, _ bool) (iac.PreviewResult, error) {
	select {
	case <-time.After(e.delay):
		return e.res, nil
	case <-ctx.Done():
		return iac.PreviewResult{}, ctx.Err()
	}
}

func runOneTimeout(engine Engine, timeout time.Duration) (Item, Event) {
	opts := Options{
		Engine:          engine,
		Redactor:        redact.New(),
		PerStackTimeout: timeout,
	}
	item, ev, _, _ := runOne(context.Background(), opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	return item, ev
}

// TestRunOnePerStackTimeout verifies a stack whose check exceeds
// timeout_per_stack classifies as a check error with the timeout reason, and
// that a fast check (or an unset timeout) is unaffected.
func TestRunOnePerStackTimeout(t *testing.T) {
	// Slow check that overruns the timeout → error / check_failed, timeout reason.
	item, ev := runOneTimeout(slowEngine{delay: time.Hour}, 20*time.Millisecond)
	if item.Outcome != OutcomeError || ev != EventCheckFailed {
		t.Fatalf("timed-out check must be error/check_failed, got outcome=%s ev=%s", item.Outcome, ev)
	}
	if !strings.Contains(item.Error, "timeout_per_stack=20ms") {
		t.Fatalf("timeout error must name timeout_per_stack, got %q", item.Error)
	}

	// Fast check under the timeout → unaffected (drift detected normally).
	res := iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn:pulumi:prod::app::aws:s3/bucket:Bucket::data"},
	}
	item, ev = runOneTimeout(slowEngine{delay: 0, res: res}, time.Minute)
	if item.Outcome != OutcomeDriftDetected || ev != EventDriftDetected {
		t.Fatalf("fast check under timeout must be unaffected, got outcome=%s ev=%s", item.Outcome, ev)
	}

	// Unset timeout → no bound: a slow-but-completing check still succeeds.
	item, ev = runOneTimeout(slowEngine{delay: 5 * time.Millisecond, res: res}, 0)
	if item.Outcome != OutcomeDriftDetected || ev != EventDriftDetected {
		t.Fatalf("unset timeout must impose no bound, got outcome=%s ev=%s", item.Outcome, ev)
	}
}

// TestRunTimeoutContinuesOtherStacks verifies a timed-out stack does not abort
// the whole run: other stacks still complete.
func TestRunTimeoutContinuesOtherStacks(t *testing.T) {
	// Engine enumerates two stacks; the check is slow so only the fast-path
	// (timeout) matters here. We drive Run with a per-stack timeout and a
	// slow engine, then assert both stacks produced items (none aborted).
	eng := multiStackSlowEngine{
		stacks: []discovery.Stack{
			{Project: "p", Name: "a", Path: "p/a", Env: "prod"},
			{Project: "p", Name: "b", Path: "p/b", Env: "prod"},
		},
		delay: time.Hour,
	}
	out, err := Run(context.Background(), Options{
		Engine:   eng,
		Redactor: redact.New(),
		Decls: []discovery.Declaration{
			{Path: "p/a", Stacks: []string{"a"}},
			{Path: "p/b", Stacks: []string{"b"}},
		},
		PerStackTimeout: 20 * time.Millisecond,
		Parallel:        2,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 items (run must continue past a timeout), got %d", len(out.Items))
	}
	for _, it := range out.Items {
		if it.Outcome != OutcomeError {
			t.Fatalf("stack %s should have timed out to error, got %s", it.Ref(), it.Outcome)
		}
	}
}

type multiStackSlowEngine struct {
	stacks []discovery.Stack
	delay  time.Duration
}

func (multiStackSlowEngine) Name() string { return "multi" }
func (e multiStackSlowEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return e.stacks, nil
}
func (e multiStackSlowEngine) DriftCheck(ctx context.Context, _ discovery.Stack, _ iac.PreviewOpts, _ bool) (iac.PreviewResult, error) {
	select {
	case <-time.After(e.delay):
		return iac.PreviewResult{}, nil
	case <-ctx.Done():
		return iac.PreviewResult{}, ctx.Err()
	}
}

// TestRunOneBaselineBootstrapSilent verifies baseline mode records
// pre-existing drift silently on the first run (no event) rather than firing.
func TestRunOneBaselineBootstrapSilent(t *testing.T) {
	res := iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn:pulumi:prod::app::aws:s3/bucket:Bucket::data"},
	}
	item, ev := runOneWith(fakeEngine{res: res}, "baseline")
	if item.Outcome != OutcomeDriftDetected {
		t.Fatalf("baseline still records the drift outcome, got %s", item.Outcome)
	}
	if ev != EventNone {
		t.Fatalf("baseline first run must be silent, got %s", ev)
	}
	if item.Fingerprint == "" {
		t.Fatal("baseline must persist the drift fingerprint")
	}
}
