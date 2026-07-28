package hcl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/iac"
)

const planJSONNoChanges = `{"format_version":"1.2","resource_changes":[],"resource_drift":[]}`

// One resource whose live state no longer matches what state records - the
// shape a refresh-only plan reports.
const planJSONDrift = `{
  "format_version": "1.2",
  "resource_drift": [
    {"address": "random_pet.name", "type": "random_pet", "name": "name",
     "change": {"actions": ["update"], "before": {"length": 2}, "after": {"length": 3},
                "after_unknown": {}, "before_sensitive": false, "after_sensitive": {}}}
  ]
}`

// A preview asked to save its plan writes it at the requested path, leaves
// it there, and says so - the file is the caller's to upload.
func TestPreviewSavesPlanAtRequestedPath(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2},
		"show -json": {stdout: planJSONChanges},
	})
	e := testEngine(fake, schemas.StackDecl{Path: "envs/net", Stacks: []string{"prod"}})
	want := filepath.Join(t.TempDir(), "saved.tfplan")
	// The faked CLI writes nothing, so stand in for what `plan -out` would
	// leave behind. PlanPath is only claimed when the file is really there.
	if err := os.WriteFile(want, []byte("plan bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := e.Preview(context.Background(), testStack, iac.PreviewOpts{
		Cwd: "/repo/envs/net", SavePlanPath: want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected preview error: %s", res.Error)
	}
	if res.PlanPath != want {
		t.Errorf("PlanPath = %q, want %q", res.PlanPath, want)
	}
	var planned string
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "plan ") {
			planned = line
		}
	}
	if !strings.Contains(planned, "-out="+want) {
		t.Errorf("plan did not target the requested path:\n%s", planned)
	}
}

// Without SavePlanPath the plan file is a temp that does not survive the
// call, and PlanPath stays empty so no caller can mistake a deleted temp for
// a durable artifact.
func TestPreviewWithoutSavePlanPathReportsNoPlan(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2},
		"show -json": {stdout: planJSONChanges},
	})
	e := testEngine(fake)

	res, err := e.Preview(context.Background(), testStack, iac.PreviewOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanPath != "" {
		t.Errorf("PlanPath = %q, want empty when no save was requested", res.PlanPath)
	}
}

// A failed plan must not claim a saved plan: an apply locked to a file the
// plan step never wrote would fail confusingly at the engine instead of
// cleanly here.
func TestPreviewFailureReportsNoSavedPlan(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan": {exit: 1, stderr: "Error: Invalid resource type"},
	})
	e := testEngine(fake)

	res, err := e.Preview(context.Background(), testStack, iac.PreviewOpts{
		Cwd: "/x", SavePlanPath: filepath.Join(t.TempDir(), "saved.tfplan"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("a failed plan must populate Error")
	}
	if res.PlanPath != "" {
		t.Errorf("PlanPath = %q, want empty after a failed plan", res.PlanPath)
	}
}

// The point of plan locking: an apply handed a plan file applies THAT file
// and runs no plan of its own. A `plan` command here would mean the engine
// recomputed the change set, which is exactly what locking forbids.
func TestApplyWithLockedPlanDoesNotReplan(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"show -json": {stdout: planJSONChanges},
		"apply":      {exit: 0},
	})
	e := testEngine(fake)
	locked := filepath.Join(t.TempDir(), "locked.tfplan")

	res, err := e.Apply(context.Background(), testStack, iac.ApplyOpts{Cwd: "/x", PlanPath: locked})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected apply error: %s", res.Error)
	}
	lines := fake.commandLines()
	for _, line := range lines {
		if strings.HasPrefix(line, "plan ") {
			t.Errorf("apply re-planned despite a locked plan:\n%s", strings.Join(lines, "\n"))
		}
	}
	found := false
	for _, line := range lines {
		if line == "apply -input=false -no-color "+locked {
			found = true
		}
	}
	if !found {
		t.Errorf("apply did not consume the locked plan:\n%s", strings.Join(lines, "\n"))
	}
	// Counts still come from the plan file, so the PR comment reports the
	// change set that was executed rather than nothing.
	if res.Counts.Add != 1 {
		t.Errorf("counts must be read from the locked plan, got %+v", res.Counts)
	}
}

// Without a locked plan, apply plans first - the pre-existing behavior,
// asserted so turning plan locking off is a real fallback and not a
// silently broken path.
func TestApplyWithoutLockedPlanStillPlans(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2},
		"show -json": {stdout: planJSONChanges},
	})
	e := testEngine(fake)

	if _, err := e.Apply(context.Background(), testStack, iac.ApplyOpts{Cwd: "/x"}); err != nil {
		t.Fatal(err)
	}
	planned := false
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "plan ") {
			planned = true
		}
	}
	if !planned {
		t.Error("apply without a locked plan must compute one")
	}
}

// Refresh and a locked plan are mutually exclusive: a refresh changes the
// diff, which is the one thing the saved plan pins. Refuse rather than
// silently dropping whichever the caller cared about less.
func TestApplyRejectsRefreshWithLockedPlan(t *testing.T) {
	fake := newFake(t, nil)
	e := testEngine(fake)

	res, err := e.Apply(context.Background(), testStack, iac.ApplyOpts{
		Cwd: "/x", PlanPath: "/tmp/locked.tfplan", Refresh: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "refresh cannot be combined with a locked plan") {
		t.Errorf("want a refusal naming the conflict, got %q", res.Error)
	}
	if len(fake.calls) != 0 {
		t.Errorf("the conflict must be caught before any CLI call, got %v", fake.commandLines())
	}
}

// A dry-run refresh stops after reading the refresh-only plan. An `apply`
// here would commit the reconciliation the caller explicitly asked not to.
func TestRefreshDryRunNeverApplies(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 2},
		"show -json":         {stdout: planJSONDrift},
	})
	e := testEngine(fake)

	res, err := e.Refresh(context.Background(), testStack, iac.RefreshOpts{Cwd: "/x", PreviewOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected refresh error: %s", res.Error)
	}
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "apply") {
			t.Errorf("PreviewOnly refresh wrote state:\n%s", strings.Join(fake.commandLines(), "\n"))
		}
	}
	if res.Counts.Total() == 0 {
		t.Error("a dry-run refresh must still report what it would reconcile")
	}
}

// A writing refresh commits the refresh-only plan it just read.
func TestRefreshCommitsTheReconciliation(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 2},
		"show -json":         {stdout: planJSONDrift},
		"apply":              {exit: 0},
	})
	e := testEngine(fake)

	if _, err := e.Refresh(context.Background(), testStack, iac.RefreshOpts{Cwd: "/x"}); err != nil {
		t.Fatal(err)
	}
	applied := false
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "apply -input=false -no-color ") {
			applied = true
		}
	}
	if !applied {
		t.Errorf("refresh did not commit the reconciliation:\n%s", strings.Join(fake.commandLines(), "\n"))
	}
}

// Nothing drifted: there is no reconciliation to commit, so no state write
// happens at all.
func TestRefreshWithNoDriftWritesNothing(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 0},
		"show -json":         {stdout: planJSONNoChanges},
	})
	e := testEngine(fake)

	res, err := e.Refresh(context.Background(), testStack, iac.RefreshOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected refresh error: %s", res.Error)
	}
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "apply") {
			t.Errorf("a no-drift refresh must not write state:\n%s", strings.Join(fake.commandLines(), "\n"))
		}
	}
}
