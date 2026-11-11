package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/redact"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/policy"
)

func boolPtr(b bool) *bool { return &b }

// TestHooksFromEngineRequiredDefaultsTrue: an omitted `required:` must
// resolve to required=true (fail closed), matching docs/policy-hooks.md.
// `required: false` stays an explicit opt-out.
func TestHooksFromEngineRequiredDefaultsTrue(t *testing.T) {
	e := &schemas.Engine{Engine: schemas.EngineBody{
		PolicyHooks: []schemas.PolicyHookYAML{
			{Name: "default", Command: []string{"opa"}},
			{Name: "explicit-true", Command: []string{"opa"}, Required: boolPtr(true)},
			{Name: "opt-out", Command: []string{"opa"}, Required: boolPtr(false)},
		},
	}}
	hooks := HooksFromEngine(e)
	if len(hooks) != 3 {
		t.Fatalf("hooks: %d", len(hooks))
	}
	if !hooks[0].Required {
		t.Error("omitted required: must default to true (fail closed)")
	}
	if !hooks[1].Required {
		t.Error("explicit required: true lost")
	}
	if hooks[2].Required {
		t.Error("explicit required: false must remain an opt-out")
	}
}

// TestPolicyHookMissingBinaryFailsClosedByDefault is the attack scenario:
// a hook declared WITHOUT `required:` whose scanner binary is absent must
// FAIL the run, not silently skip the policy gate.
func TestPolicyHookMissingBinaryFailsClosedByDefault(t *testing.T) {
	e := &schemas.Engine{Engine: schemas.EngineBody{
		PolicyHooks: []schemas.PolicyHookYAML{
			{Name: "scanner", Command: []string{"/definitely/not/a/real/scanner-xyz", "{{plan_json}}"}},
		},
	}}
	hooks := HooksFromEngine(e)
	res := policy.Run(context.Background(), hooks[0], policy.Context{}, redact.New())
	if res.Outcome != "fail" {
		t.Fatalf("missing binary without explicit required must fail, got %q (%+v)", res.Outcome, res)
	}
	passed, _ := policy.Aggregate([]policy.Result{res})
	if passed {
		t.Fatal("aggregate must report the run as blocked")
	}
}

// TestWritePlanJSONSlugsNamesIntoTheTempDir pins the path fix: the plan
// filename is built from slugged project and stack names, so `..` or `/`
// in a repo-controlled name cannot escape the temp dir. The old filename
// composed the names verbatim through filepath.Join, which cleaned
// traversal - a crafted stack name targeted any writable path.
func TestWritePlanJSONSlugsNamesIntoTheTempDir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		project string
		stack   string
	}{
		{"dotdot project", "../escape", "stack"},
		{"dotdot stack", "proj", "../evil"},
		{"slash in project", "a/b", "c"},
		{"slash in stack", "proj", "c/d"},
		{"traversal out of tmp", "../../etc", "x/y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss := summary.StackSummary{Project: c.project, Stack: c.stack, FullPlan: `{"version":1}`}
			path, dir, err := writePlanJSON(ss)
			if err != nil {
				t.Fatalf("writePlanJSON(%q, %q): %v", c.project, c.stack, err)
			}
			defer os.RemoveAll(dir)

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				t.Fatalf("rel: %v", err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("plan path %q escaped the temp dir %q", path, dir)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("plan file not written: %v", err)
			}
		})
	}

	// Ordinary names keep their existing human-readable filename.
	path, dir, err := writePlanJSON(summary.StackSummary{Project: "proj", Stack: "dev", FullPlan: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if want := filepath.Join(dir, "proj-dev.json"); path != want {
		t.Fatalf("ordinary names must keep their filename: got %q, want %q", path, want)
	}
}

// TestRunPolicyForStackPlanWriteFailureDenies pins the fail-closed refusal:
// when the plan file cannot be written, no hook runs, the run is denied,
// and the error names the hooks, the stack, the rule, and the failing
// condition - never a bare "error".
func TestRunPolicyForStackPlanWriteFailureDenies(t *testing.T) {
	// Force the temp-dir creation to fail so the write path errors before
	// any hook runs. (t.Setenv forbids t.Parallel.)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	hooks := []policy.Hook{{Name: "conftest", Command: []string{"true"}, Required: true}}
	s := discovery.Stack{Project: "proj", Name: "evil/stack", Env: "prod"}

	_, _, err := RunPolicyForStack(context.Background(), hooks, s, summary.StackSummary{FullPlan: "{}"}, redact.New())
	if err == nil {
		t.Fatal("plan write failure must deny the policy gate")
	}
	want := "policy hooks conftest not run for proj/evil/stack: plan file could not be written"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal must name the hooks, the stack, the rule, and the failing condition:\nwant: %s\ngot:  %v", want, err)
	}
}
