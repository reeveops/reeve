package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/drift"
)

// driftRepo scaffolds a repo via init and chdirs into it.
func driftRepo(t *testing.T) string {
	t.Helper()
	fakeTTY(t, false)
	root := pulumiRepo(t)
	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	return root
}

func writeDriftConfig(t *testing.T, root, body string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, ".reeve", "drift.yaml"), body)
}

func TestDriftRunUnknownScheduleNoSchedulesConfigured(t *testing.T) {
	driftRepo(t)
	_, err := runReeve(t, "drift", "run", "--schedule", "nightly")
	if err == nil || !strings.Contains(err.Error(), `unknown schedule "nightly": no schedules configured`) {
		t.Fatalf("err = %v", err)
	}
}

func TestDriftRunUnknownScheduleListsConfigured(t *testing.T) {
	root := driftRepo(t)
	writeDriftConfig(t, root, `version: 1
config_type: drift
schedules:
  hourly:
    patterns: ["projects/*"]
  weekly:
    patterns: ["projects/api/*"]
`)
	// A typo must fail closed (never fall back to the global scope) and
	// name the configured schedules.
	_, err := runReeve(t, "drift", "run", "--schedule", "hourlyy")
	if err == nil || !strings.Contains(err.Error(), `unknown schedule "hourlyy"`) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "hourly, weekly") {
		t.Errorf("error should list configured schedules sorted: %v", err)
	}
}

func TestDriftReportNoRuns(t *testing.T) {
	driftRepo(t)
	out, err := runReeve(t, "drift", "report")
	if err != nil {
		t.Fatalf("drift report: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no drift runs found") {
		t.Errorf("expected empty-state notice:\n%s", out)
	}
}

func TestDriftStatusEmpty(t *testing.T) {
	driftRepo(t)
	out, err := runReeve(t, "drift", "status")
	if err != nil {
		t.Fatalf("drift status: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output for empty state:\n%s", out)
	}
}

func TestDriftSuppressLifecycle(t *testing.T) {
	driftRepo(t)

	if out, err := runReeve(t, "drift", "suppress", "add", "projects/api/dev",
		"--until", "48h", "--reason", "planned maintenance"); err != nil {
		t.Fatalf("suppress add: %v\n%s", err, out)
	}

	out, err := runReeve(t, "drift", "suppress", "list")
	if err != nil {
		t.Fatalf("suppress list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "projects/api/dev") || !strings.Contains(out, "reason=planned maintenance") {
		t.Errorf("active suppression not listed:\n%s", out)
	}

	if out, err := runReeve(t, "drift", "suppress", "clear", "projects/api/dev"); err != nil {
		t.Fatalf("suppress clear: %v\n%s", err, out)
	}

	out, err = runReeve(t, "drift", "suppress", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "projects/api/dev") {
		t.Errorf("cleared suppression still listed:\n%s", out)
	}
}

func TestDriftSuppressExtendedDurations(t *testing.T) {
	driftRepo(t)
	// parseDurationExtended accepts d/w suffixes plain time.ParseDuration
	// rejects.
	if out, err := runReeve(t, "drift", "suppress", "add", "projects/api/dev", "--until", "7d"); err != nil {
		t.Fatalf("suppress add 7d: %v\n%s", err, out)
	}
}

func TestDriftSuppressValidation(t *testing.T) {
	driftRepo(t)
	cases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"bad ref", []string{"drift", "suppress", "add", "not-a-ref"}, "expected project/stack"},
		{"bad duration", []string{"drift", "suppress", "add", "projects/api/dev", "--until", "soon"}, "invalid duration"},
		{"clear bad ref", []string{"drift", "suppress", "clear", "not-a-ref"}, "expected project/stack"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runReeve(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestDriftSuppressAddRequiresArg(t *testing.T) {
	driftRepo(t)
	if _, err := runReeve(t, "drift", "suppress", "add"); err == nil {
		t.Fatal("expected arg-count error")
	}
}

func TestDriftCommandsFailWithoutConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"drift", "run"},
		{"drift", "status"},
		{"drift", "report"},
	} {
		if _, err := runReeve(t, args...); err == nil {
			t.Errorf("%v should fail outside a reeve repo", args)
		}
	}
}

func TestDriftStatusReadsStoredState(t *testing.T) {
	root := driftRepo(t)
	// Seed a stored drift state blob the way a previous run would have.
	stateDir := filepath.Join(root, ".reeve-state", "drift", "state", "projects-api")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(stateDir, "dev.json"),
		`{"project":"projects-api","stack":"dev","last_outcome":"drifted","last_checked_at":"2026-01-02T03:04:05Z","ongoing_since":"2026-01-01T00:00:00Z"}`)

	out, err := runReeve(t, "drift", "status")
	if err != nil {
		t.Fatalf("drift status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "projects-api/dev") || !strings.Contains(out, "last=drifted") {
		t.Errorf("stored state not rendered:\n%s", out)
	}

	// --stack filters to an exact project/stack.
	out, err = runReeve(t, "drift", "status", "--stack", "projects-api/other")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "projects-api/dev") {
		t.Errorf("--stack filter did not exclude other stacks:\n%s", out)
	}
}

func TestDriftExitError(t *testing.T) {
	mk := func(eo schemas.DriftExitOn) *config.Config {
		return &config.Config{Drift: &schemas.Drift{Behavior: schemas.DriftBehavior{ExitOn: eo}}}
	}
	out := &drift.RunOutput{
		Items: []drift.Item{
			{Project: "a", Stack: "prod", Outcome: drift.OutcomeDriftDetected},
			{Project: "b", Stack: "prod", Outcome: drift.OutcomeDriftDetected},
			{Project: "c", Stack: "prod", Outcome: drift.OutcomeError},
		},
		Events: []drift.Event{drift.EventDriftDetected, drift.EventDriftOngoing, drift.EventCheckFailed},
	}

	// All conditions off (the default): exit 0 regardless of findings.
	if err := driftExitError(mk(schemas.DriftExitOn{}), out); err != nil {
		t.Fatalf("exit_on defaults must stay exit-0: %v", err)
	}
	// No drift.yaml at all: exit 0.
	if err := driftExitError(&config.Config{}, out); err != nil {
		t.Fatalf("nil drift config must stay exit-0: %v", err)
	}

	err := driftExitError(mk(schemas.DriftExitOn{DriftDetected: true}), out)
	if err == nil || !strings.Contains(err.Error(), "exit_on.drift_detected") {
		t.Fatalf("drift_detected condition: %v", err)
	}
	err = driftExitError(mk(schemas.DriftExitOn{DriftOngoing: true}), out)
	if err == nil || !strings.Contains(err.Error(), "exit_on.drift_ongoing") {
		t.Fatalf("drift_ongoing condition: %v", err)
	}
	err = driftExitError(mk(schemas.DriftExitOn{RunError: true}), out)
	if err == nil || !strings.Contains(err.Error(), "exit_on.run_error") {
		t.Fatalf("run_error condition: %v", err)
	}
	// Multiple conditions compose into one message.
	err = driftExitError(mk(schemas.DriftExitOn{DriftDetected: true, RunError: true}), out)
	if err == nil || !strings.Contains(err.Error(), "drift_detected") || !strings.Contains(err.Error(), "run_error") {
		t.Fatalf("composed conditions: %v", err)
	}
	// A clean run never exits nonzero, even with every condition armed.
	clean := &drift.RunOutput{
		Items:  []drift.Item{{Project: "a", Stack: "prod", Outcome: drift.OutcomeNoDrift}},
		Events: []drift.Event{drift.EventNone},
	}
	if err := driftExitError(mk(schemas.DriftExitOn{DriftDetected: true, DriftOngoing: true, RunError: true}), clean); err != nil {
		t.Fatalf("clean run must exit 0: %v", err)
	}
}

func TestBuildClassificationTreatDefaultsTrue(t *testing.T) {
	// Omitted treat_as_drift block must default both flags to true (the
	// documented default: orphaned/missing count as drift).
	c := buildClassification(schemas.DriftClassification{})
	if !c.TreatOrphanedAsDrift || !c.TreatMissingAsDrift {
		t.Fatalf("treat flags must default true when unset, got orphaned=%v missing=%v",
			c.TreatOrphanedAsDrift, c.TreatMissingAsDrift)
	}
	// An explicit false opts out.
	no := false
	c = buildClassification(schemas.DriftClassification{
		TreatAsDrift: schemas.TreatAsDrift{OrphanedState: &no},
	})
	if c.TreatOrphanedAsDrift {
		t.Fatal("explicit orphaned_state:false must disable treating orphaned as drift")
	}
	if !c.TreatMissingAsDrift {
		t.Fatal("missing_state left unset must stay true")
	}
}

func TestBuildPermanentSuppressions(t *testing.T) {
	sups := buildPermanentSuppressions([]schemas.SuppressionYAML{
		{Stack: "prod/legacy", Reason: "vendor"},
		{Stack: "prod/frozen", Until: "2099-01-01T00:00:00Z"},
		{Stack: "prod/bad", Until: "not-a-date"}, // unparseable -> permanent
		{Stack: ""},                              // dropped
	})
	if len(sups) != 3 {
		t.Fatalf("expected 3 suppressions (empty stack dropped), got %d", len(sups))
	}
	if !sups[0].Until.IsZero() {
		t.Fatal("omitted until must be permanent (zero)")
	}
	if sups[1].Until.IsZero() {
		t.Fatal("valid RFC3339 until must parse")
	}
	if !sups[2].Until.IsZero() {
		t.Fatal("unparseable until must fall back to permanent (zero), not drop the suppression")
	}
}
