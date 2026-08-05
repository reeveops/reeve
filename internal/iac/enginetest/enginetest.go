// Package enginetest is the executable form of the iac.Engine contract.
//
// Every adapter is run through the same suite, so "the contract" is a thing
// that fails CI rather than prose that drifts. The suite asserts only what
// docs/contracts/iac-engine.md promises; an adapter-specific behavior belongs
// in that adapter's own tests.
//
// The suite drives a REAL engine binary against a REAL local backend. It
// tests reeve's adapter code - argument construction, process handling, exit
// classification, plan parsing, failure shaping - not a mock of it. Each
// adapter supplies a Fixture that knows how to write its own source layout;
// every assertion below is engine-agnostic.
//
// Usage, from the adapter's package:
//
//	func TestContract(t *testing.T) {
//	    enginetest.RunContract(t, enginetest.Subject{
//	        TypeName:   "tofu",
//	        NewFixture: newTofuFixture, // skips when the binary is absent
//	    })
//	}
package enginetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// Intent is the desired state a Fixture writes into its stack source. The
// suite drives the lifecycle by moving between intents; the fixture decides
// how to express each one in its engine's language.
type Intent int

const (
	// IntentEmpty declares no resources: a plan against it is a no-op once
	// state is empty.
	IntentEmpty Intent = iota
	// IntentOne declares exactly one resource carrying the value "v1". The
	// resource's address MUST be stable across intents so a change is
	// reported as an update/replace rather than a delete+create pair.
	IntentOne
	// IntentOneChanged declares the same single resource carrying "v2".
	IntentOneChanged
	// IntentBroken declares source the engine cannot process at all
	// (syntax error, unresolvable reference). Used for the fail-closed
	// assertions.
	IntentBroken
)

// SecretEnvKey is the environment variable the suite plants a fake secret
// in before every operation. No fixture may reference it, so the value must
// never reach a result field. See TestSecretsDoNotLeak.
// #nosec G101 -- env var NAME, not a credential; the value lives in
// SecretEnvValue and is a test canary.
const SecretEnvKey = "REEVE_CONFORMANCE_SECRET"

// SecretEnvValue is the planted value. Distinctive so a substring search
// cannot false-positive on ordinary plan output.
const SecretEnvValue = "s3cr3t-conformance-canary-do-not-log"

// Fixture is the engine-specific half of the suite: a real repository on
// disk plus the knobs the shared assertions need. Implementations live in
// the adapter's own test files and skip the test when their engine binary is
// not installed.
type Fixture interface {
	// Root is the repo root the stack was written under.
	Root() string
	// Stack is the stack every operation runs against.
	Stack() discovery.Stack
	// Write rewrites the stack's source to express intent. It must be
	// callable repeatedly on the same fixture.
	Write(t *testing.T, intent Intent)
	// StateDigest returns a stable digest of the engine's persisted state
	// for this stack, or "" if the adapter's state is not inspectable. Used
	// to assert that a drift check never writes state.
	StateDigest(t *testing.T) string
}

// Subject is one adapter under test.
type Subject struct {
	// TypeName is the registry key (config engine.type).
	TypeName string
	// NewFixture builds a fresh engine + fixture pair. It is called once per
	// subtest, so no state leaks between assertions. Implementations call
	// t.Skip when their engine binary is unavailable.
	NewFixture func(t *testing.T) (iac.Engine, Fixture)
}

// RunContract runs every contract assertion against the subject.
func RunContract(t *testing.T, s Subject) {
	t.Helper()
	t.Run("Registry", func(t *testing.T) { testRegistry(t, s) })
	t.Run("Identity", func(t *testing.T) { testIdentity(t, s) })
	t.Run("Enumerate", func(t *testing.T) { testEnumerate(t, s) })
	t.Run("PreviewNoop", func(t *testing.T) { testPreviewNoop(t, s) })
	t.Run("PreviewCreate", func(t *testing.T) { testPreviewCreate(t, s) })
	t.Run("PreviewFailureIsNotAnError", func(t *testing.T) { testPreviewFailure(t, s) })
	t.Run("ApplyConverges", func(t *testing.T) { testApplyConverges(t, s) })
	t.Run("ApplyReportsUpdate", func(t *testing.T) { testApplyReportsUpdate(t, s) })
	t.Run("DriftCheckFailsClosed", func(t *testing.T) { testDriftFailsClosed(t, s) })
	t.Run("DriftCheckDoesNotWriteState", func(t *testing.T) { testDriftReadOnly(t, s) })
	t.Run("SavedPlanIsWrittenAndApplied", func(t *testing.T) { testSavedPlanRoundTrip(t, s) })
	t.Run("SavedPlanIgnoredWithoutCapability", func(t *testing.T) { testSavedPlanOptional(t, s) })
	t.Run("RefreshDryRunDoesNotWriteState", func(t *testing.T) { testRefreshDryRun(t, s) })
	t.Run("RefreshConverges", func(t *testing.T) { testRefreshConverges(t, s) })
	t.Run("SecretsDoNotLeak", func(t *testing.T) { testSecretsDoNotLeak(t, s) })
}

// env returns the option env every operation runs with: the planted secret
// plus anything the caller adds.
func env() map[string]string {
	return map[string]string{SecretEnvKey: SecretEnvValue}
}

func previewOpts(f Fixture) iac.PreviewOpts {
	return iac.PreviewOpts{Cwd: stackDir(f), Env: env(), TimeoutSec: 300}
}

func applyOpts(f Fixture) iac.ApplyOpts {
	return iac.ApplyOpts{Cwd: stackDir(f), Env: env(), TimeoutSec: 300}
}

// stackDir is the absolute working directory for the stack. Stack.Path is
// repo-relative by contract, so the suite joins it onto the fixture root
// rather than assuming the adapter will.
func stackDir(f Fixture) string {
	return filepath.Join(f.Root(), f.Stack().Path)
}

// --- contract assertions ------------------------------------------------

// The registry resolves an engine purely by its type string, and an
// unknown type is an error that names the registered set.
func testRegistry(t *testing.T, s Subject) {
	e, err := iac.New(schemas.EngineBody{Type: s.TypeName})
	if err != nil {
		t.Fatalf("iac.New(%q) failed: %v", s.TypeName, err)
	}
	if e == nil {
		t.Fatalf("iac.New(%q) returned a nil engine and no error", s.TypeName)
	}
	if _, err := iac.New(schemas.EngineBody{Type: "definitely-not-an-engine"}); err == nil {
		t.Error("iac.New with an unregistered type must error")
	} else if !strings.Contains(err.Error(), s.TypeName) {
		t.Errorf("unknown-type error must name the registered set (missing %q): %v", s.TypeName, err)
	}
}

// Name() is display-only but must be non-empty, and Capabilities() must be
// a pure function of the engine - core reads it more than once per run.
func testIdentity(t *testing.T, s Subject) {
	e, _ := s.NewFixture(t)
	if strings.TrimSpace(e.Name()) == "" {
		t.Error("Name() must be non-empty: it labels every rendered result")
	}
	if got, want := e.Capabilities(), e.Capabilities(); !capsEqual(got, want) {
		t.Error("Capabilities() must be stable across calls")
	}
}

// EnumerateStacks is deterministic and idempotent: the run pipeline calls it
// on every preview and apply, and an unstable order reshuffles the PR
// comment on every push.
func testEnumerate(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentOne)
	ctx := context.Background()

	first, err := e.EnumerateStacks(ctx, f.Root())
	if err != nil {
		t.Fatalf("EnumerateStacks: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("EnumerateStacks found no stacks in a fixture that declares one")
	}
	second, err := e.EnumerateStacks(ctx, f.Root())
	if err != nil {
		t.Fatalf("EnumerateStacks (second call): %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("EnumerateStacks is not idempotent: %d then %d stacks", len(first), len(second))
	}
	seen := map[string]bool{}
	for i := range first {
		if first[i].Ref() != second[i].Ref() {
			t.Errorf("EnumerateStacks order is not stable at %d: %q then %q",
				i, first[i].Ref(), second[i].Ref())
		}
		if first[i].Project == "" || first[i].Name == "" {
			t.Errorf("stack %d has an empty Project or Name: %+v", i, first[i])
		}
		if seen[first[i].Ref()] {
			t.Errorf("EnumerateStacks returned %q twice", first[i].Ref())
		}
		seen[first[i].Ref()] = true
	}
	if i, j := 0, len(first)-1; first[i].Ref() > first[j].Ref() {
		t.Errorf("EnumerateStacks must be sorted by Ref; got %q before %q", first[i].Ref(), first[j].Ref())
	}
}

// A preview with nothing to do reports zero counts and no error. The run
// pipeline treats this as a no-op stack and suppresses noise for it, so a
// spurious count here produces a phantom plan in the PR comment.
func testPreviewNoop(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentEmpty)

	res, err := e.Preview(context.Background(), f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview returned a transport error on a valid no-op stack: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Preview reported a per-stack failure on a valid no-op stack: %s", res.Error)
	}
	if total := res.Counts.Add + res.Counts.Change + res.Counts.Delete + res.Counts.Replace; total != 0 {
		t.Errorf("no-op preview must report zero counts, got %+v", res.Counts)
	}
	if len(res.Resources) != 0 {
		t.Errorf("no-op preview must report no resource changes, got %d", len(res.Resources))
	}
}

// A preview that creates one resource reports it in Counts and renders a
// non-empty PlanSummary.
//
// Note on Resources: the interface documents it as best-effort and
// drift-oriented (Pulumi detailedDiff, Terraform resource_drift), and the
// drift runner is its only consumer - the preview path is not required to
// fill it. So this asserts SHAPE when an adapter populates it, not presence.
// Demanding presence here would fail a compliant adapter; ignoring the shape
// would let a malformed entry through to classification filtering.
func testPreviewCreate(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentOne)

	res, err := e.Preview(context.Background(), f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Preview reported a failure on a valid stack: %s", res.Error)
	}
	if res.Counts.Add != 1 {
		t.Errorf("expected Counts.Add == 1, got %+v", res.Counts)
	}
	if strings.TrimSpace(res.PlanSummary) == "" {
		t.Error("PlanSummary must be non-empty for a plan with changes: it is what the PR comment renders")
	}
	assertResourceShape(t, res.Resources)
}

// assertResourceShape validates every populated ResourceChange. Address feeds
// ignore_resources globs, Op and Category feed treat_as_drift, so a malformed
// entry makes drift classification behave unpredictably rather than loudly.
// An empty slice is legal - see the note on testPreviewCreate.
func assertResourceShape(t *testing.T, changes []iac.ResourceChange) {
	t.Helper()
	for i, rc := range changes {
		if rc.Address == "" {
			t.Errorf("Resources[%d].Address is empty: ignore_resources globs match on it", i)
		}
		if !validOp(rc.Op) {
			t.Errorf("Resources[%d].Op = %q, want one of create|update|delete|replace", i, rc.Op)
		}
		if !validCategory(rc.Category) {
			t.Errorf("Resources[%d].Category = %q, want one of changed|orphaned|missing", i, rc.Category)
		}
	}
}

// Per-stack failure is reported in PreviewResult.Error with a NIL returned
// error. run.Preview distinguishes "this stack failed" (render it as failed,
// keep going) from "the engine could not be driven at all"; collapsing the
// two aborts the whole run on one bad stack.
//
// Error must also be non-empty. The drift runner's fail-closed contract
// keys on that: an empty Error on a failed check reads as "no drift" and
// falsely resolves an active alert.
func testPreviewFailure(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentBroken)

	res, err := e.Preview(context.Background(), f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview must report a broken stack via Error, not a returned error: %v", err)
	}
	if strings.TrimSpace(res.Error) == "" {
		t.Fatal("Preview of a broken stack returned an empty Error: downstream reads that as success")
	}
	if total := res.Counts.Add + res.Counts.Change + res.Counts.Delete + res.Counts.Replace; total != 0 {
		t.Errorf("a failed preview must not report counts, got %+v", res.Counts)
	}
}

// Apply actually converges: after applying a create, the next preview is a
// no-op. This is the end-to-end proof that the adapter's plan and apply
// paths agree - a mismatch here means reeve previews one thing and applies
// another.
func testApplyConverges(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentOne)
	ctx := context.Background()

	ares, err := e.Apply(ctx, f.Stack(), applyOpts(f))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ares.Error != "" {
		t.Fatalf("Apply failed on a valid stack: %s\n%s", ares.Error, ares.Output)
	}
	if ares.Counts.Add != 1 {
		t.Errorf("Apply must report the change set it executed, got %+v", ares.Counts)
	}
	if ares.DurationMS <= 0 {
		t.Error("ApplyResult.DurationMS must be populated")
	}

	after, err := e.Preview(ctx, f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview after Apply: %v", err)
	}
	if after.Error != "" {
		t.Fatalf("Preview after Apply reported a failure: %s", after.Error)
	}
	total := after.Counts.Add + after.Counts.Change + after.Counts.Delete + after.Counts.Replace
	if total != 0 {
		t.Errorf("Apply did not converge: the following preview still reports %+v", after.Counts)
	}
}

// An in-place change is reported as an update (or replace, if the engine
// cannot do it in place) and carries the changed property path. Paths is
// what classification.ignore_properties matches on, so an adapter that
// leaves it empty makes per-property drift suppression a silent no-op.
func testApplyReportsUpdate(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	ctx := context.Background()

	f.Write(t, IntentOne)
	if ares, err := e.Apply(ctx, f.Stack(), applyOpts(f)); err != nil || ares.Error != "" {
		t.Fatalf("seed Apply failed: err=%v result=%s", err, ares.Error)
	}

	f.Write(t, IntentOneChanged)
	res, err := e.Preview(ctx, f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview after change: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Preview after change reported a failure: %s", res.Error)
	}
	if res.Counts.Change+res.Counts.Replace != 1 {
		t.Fatalf("expected exactly one update or replace, got %+v", res.Counts)
	}
	if strings.TrimSpace(res.PlanSummary) == "" {
		t.Error("PlanSummary must be non-empty for an update: it is what the PR comment renders")
	}
	assertResourceShape(t, res.Resources)
}

// DriftCheck fails closed, and this is the ONE place the contract differs
// from Preview: a check that cannot produce a verdict returns a non-empty
// Error AND a non-nil error. The drift runner keys on the non-nil error to
// record a failed check; without it an unreadable check is indistinguishable
// from "no drift" and would resolve a live alert.
func testDriftFailsClosed(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	f.Write(t, IntentBroken)

	res, err := e.DriftCheck(context.Background(), f.Stack(), previewOpts(f), true)
	if err == nil {
		t.Error("DriftCheck that produced no plan must return a non-nil error (fail closed)")
	}
	if strings.TrimSpace(res.Error) == "" {
		t.Error("DriftCheck that produced no plan must return a non-empty Error")
	}
}

// A drift check never writes engine state. Drift runs on a schedule against
// production with no approval in front of them; a check that mutates state
// turns observation into an unreviewed change.
func testDriftReadOnly(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	ctx := context.Background()

	f.Write(t, IntentOne)
	if ares, err := e.Apply(ctx, f.Stack(), applyOpts(f)); err != nil || ares.Error != "" {
		t.Fatalf("seed Apply failed: err=%v result=%s", err, ares.Error)
	}

	before := f.StateDigest(t)
	if before == "" {
		t.Skip("fixture does not expose state; cannot assert read-only drift")
	}
	if _, err := e.DriftCheck(ctx, f.Stack(), previewOpts(f), true); err != nil {
		t.Fatalf("DriftCheck on a converged stack: %v", err)
	}
	if after := f.StateDigest(t); after != before {
		t.Error("DriftCheck mutated engine state; a drift check must be read-only")
	}
}

// Plan locking round-trip: a preview asked to save a plan produces a real
// artifact, and an apply handed that artifact executes it. This is the whole
// promise of SupportsSavedPlans - without it, apply re-plans and what ships
// is whatever the world looks like at apply time rather than what was
// reviewed.
func testSavedPlanRoundTrip(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	if !e.Capabilities().SupportsSavedPlans {
		t.Skip("engine does not support saved plans")
	}
	ctx := context.Background()
	f.Write(t, IntentOne)

	planPath := filepath.Join(t.TempDir(), "saved.plan")
	popts := previewOpts(f)
	popts.SavePlanPath = planPath
	res, err := e.Preview(ctx, f.Stack(), popts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Preview failed on a valid stack: %s", res.Error)
	}
	if res.PlanPath == "" {
		t.Fatal("SupportsSavedPlans is true but Preview reported no PlanPath; plan locking would silently degrade to a re-plan")
	}
	if res.PlanPath != planPath {
		t.Errorf("PlanPath = %q, want the requested %q", res.PlanPath, planPath)
	}
	fi, statErr := os.Stat(res.PlanPath)
	if statErr != nil {
		t.Fatalf("PlanPath does not exist: %v", statErr)
	}
	if fi.Size() == 0 {
		t.Fatal("saved plan is empty; an apply locked to it could not execute anything")
	}

	aopts := applyOpts(f)
	aopts.PlanPath = res.PlanPath
	ares, err := e.Apply(ctx, f.Stack(), aopts)
	if err != nil {
		t.Fatalf("Apply with a saved plan: %v", err)
	}
	if ares.Error != "" {
		t.Fatalf("Apply of a freshly saved plan failed: %s\n%s", ares.Error, ares.Output)
	}
	if ares.Counts.Add != 1 {
		t.Errorf("the locked apply must execute the saved change set, got %+v", ares.Counts)
	}

	// And it converged: the plan was applied, not merely accepted.
	after, err := e.Preview(ctx, f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview after locked Apply: %v", err)
	}
	if total := after.Counts.Add + after.Counts.Change + after.Counts.Delete + after.Counts.Replace; total != 0 {
		t.Errorf("locked apply did not converge: next preview reports %+v", after.Counts)
	}
}

// SavePlanPath is a request, not a demand: an engine that cannot save plans
// must ignore it and still produce a normal preview. Failing the preview
// instead would take plan locking from "degrades to a re-plan" to "breaks
// every PR the moment it is enabled".
func testSavedPlanOptional(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	if e.Capabilities().SupportsSavedPlans {
		t.Skip("engine supports saved plans; covered by the round-trip assertion")
	}
	f.Write(t, IntentOne)

	popts := previewOpts(f)
	popts.SavePlanPath = filepath.Join(t.TempDir(), "saved.plan")
	res, err := e.Preview(context.Background(), f.Stack(), popts)
	if err != nil {
		t.Fatalf("Preview with SavePlanPath on a non-saving engine must not error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Preview with SavePlanPath on a non-saving engine must still succeed: %s", res.Error)
	}
	if res.PlanPath != "" {
		t.Errorf("PlanPath = %q, want empty: an engine without the capability must not claim a saved plan", res.PlanPath)
	}
}

// A dry-run refresh writes no state. This is the same promise the drift
// check makes, and for the same reason: `/reeve refresh --dry-run` is sold
// as a read, so an adapter that quietly commits the reconciliation turns an
// inspection into an unreviewed state rewrite.
func testRefreshDryRun(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	if !e.Capabilities().SupportsRefresh {
		t.Skip("engine does not support refresh")
	}
	ctx := context.Background()

	f.Write(t, IntentOne)
	if ares, err := e.Apply(ctx, f.Stack(), applyOpts(f)); err != nil || ares.Error != "" {
		t.Fatalf("seed Apply failed: err=%v result=%s", err, ares.Error)
	}
	before := f.StateDigest(t)
	if before == "" {
		t.Skip("fixture does not expose state; cannot assert read-only refresh")
	}

	res, err := e.Refresh(ctx, f.Stack(), iac.RefreshOpts{
		Cwd: stackDir(f), Env: env(), TimeoutSec: 300, PreviewOnly: true,
	})
	if err != nil {
		t.Fatalf("Refresh --preview-only: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Refresh --preview-only failed on a converged stack: %s\n%s", res.Error, res.Output)
	}
	if after := f.StateDigest(t); after != before {
		t.Error("PreviewOnly refresh mutated engine state; it must be read-only")
	}
}

// A real refresh on a converged stack reconciles nothing and reports
// nothing. Counts here describe state reconciliation, so a converged stack
// producing non-zero counts means the adapter is reporting the wrong thing -
// and the refresh comment would tell operators resources changed when none
// did.
func testRefreshConverges(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	if !e.Capabilities().SupportsRefresh {
		t.Skip("engine does not support refresh")
	}
	ctx := context.Background()

	f.Write(t, IntentOne)
	if ares, err := e.Apply(ctx, f.Stack(), applyOpts(f)); err != nil || ares.Error != "" {
		t.Fatalf("seed Apply failed: err=%v result=%s", err, ares.Error)
	}

	res, err := e.Refresh(ctx, f.Stack(), iac.RefreshOpts{
		Cwd: stackDir(f), Env: env(), TimeoutSec: 300,
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Refresh failed on a converged stack: %s\n%s", res.Error, res.Output)
	}
	if total := res.Counts.Add + res.Counts.Change + res.Counts.Delete + res.Counts.Replace; total != 0 {
		t.Errorf("refresh of a converged stack must reconcile nothing, got %+v", res.Counts)
	}
	if res.DurationMS <= 0 {
		t.Error("RefreshResult.DurationMS must be populated")
	}

	// The next preview is still a no-op: a refresh changes reeve's record of
	// infrastructure, never the infrastructure.
	after, err := e.Preview(ctx, f.Stack(), previewOpts(f))
	if err != nil {
		t.Fatalf("Preview after Refresh: %v", err)
	}
	if total := after.Counts.Add + after.Counts.Change + after.Counts.Delete + after.Counts.Replace; total != 0 {
		t.Errorf("refresh disturbed a converged stack: next preview reports %+v", after.Counts)
	}
}

// Nothing from the process environment leaks into a result. Every result
// field is persisted to the blob store and most are rendered into a public
// PR comment, while opts.Env carries short-lived cloud credentials.
func testSecretsDoNotLeak(t *testing.T, s Subject) {
	e, f := s.NewFixture(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		intent Intent
	}{
		{"create", IntentOne},
		{"broken", IntentBroken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.Write(t, tc.intent)
			res, _ := e.Preview(ctx, f.Stack(), previewOpts(f))
			for field, v := range map[string]string{
				"PlanSummary": res.PlanSummary,
				"PlanDiff":    res.PlanDiff,
				"FullPlan":    res.FullPlan,
				"Error":       res.Error,
			} {
				if strings.Contains(v, SecretEnvValue) {
					t.Errorf("PreviewResult.%s leaked the value of %s", field, SecretEnvKey)
				}
			}
		})
	}
}

func validOp(op string) bool {
	switch op {
	case "create", "update", "delete", "replace":
		return true
	}
	return false
}

func validCategory(c string) bool {
	switch c {
	case iac.DriftChanged, iac.DriftOrphaned, iac.DriftMissing:
		return true
	}
	return false
}

func capsEqual(a, b iac.Capabilities) bool {
	if a.SupportsSavedPlans != b.SupportsSavedPlans ||
		a.SupportsRefresh != b.SupportsRefresh ||
		a.SupportsPolicyNative != b.SupportsPolicyNative ||
		len(a.SecretsProviderTypes) != len(b.SecretsProviderTypes) {
		return false
	}
	for i := range a.SecretsProviderTypes {
		if a.SecretsProviderTypes[i] != b.SecretsProviderTypes[i] {
			return false
		}
	}
	return true
}
