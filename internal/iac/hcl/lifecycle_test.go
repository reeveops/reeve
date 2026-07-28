package hcl

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac"
)

// The engine packages import hcl, so hcl's own tests cannot import them back.
// These mirror the real dialects; terraform_test.go and tofu_test.go assert
// the real ones match, so a drift between them fails there.
var (
	testTerraform = Dialect{
		TypeName: "terraform", Display: "Terraform", Binary: "terraform",
		SourceExts: []string{".tf"},
	}
	testTofu = Dialect{
		TypeName: "tofu", Display: "OpenTofu", Binary: "tofu",
		SourceExts: []string{".tf", ".tofu"},
	}
)

// call records one faked CLI invocation.
type call struct {
	args []string
}

// fakeCLI scripts the runner seam: each key is the first args joined by
// space that it matches by prefix; the value is the canned result.
type fakeCLI struct {
	t       *testing.T
	calls   []call
	results map[string]fakeResult
}

type fakeResult struct {
	stdout string
	stderr string
	exit   int
	err    error
}

func (f *fakeCLI) run(_ context.Context, _ string, _ map[string]string, _ string, args ...string) (execResult, error) {
	f.calls = append(f.calls, call{args: args})
	joined := strings.Join(args, " ")
	for prefix, res := range f.results {
		if strings.HasPrefix(joined, prefix) {
			return execResult{Stdout: []byte(res.stdout), Stderr: []byte(res.stderr), ExitCode: res.exit}, res.err
		}
	}
	return execResult{}, nil // default: success, empty output
}

func (f *fakeCLI) commandLines() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, strings.Join(c.args, " "))
	}
	return out
}

func newFake(t *testing.T, results map[string]fakeResult) *fakeCLI {
	return &fakeCLI{t: t, results: results}
}

func testEngine(fake *fakeCLI, decls ...schemas.StackDecl) *Engine {
	e := New(testTerraform, schemas.EngineBody{Stacks: decls})
	e.run = fake.run
	return e
}

var testStack = discovery.Stack{Project: "net", Path: "envs/net", Name: "prod", Env: "prod"}

const planJSONChanges = `{
  "format_version": "1.2",
  "resource_changes": [
    {"address": "random_pet.name", "type": "random_pet", "name": "name",
     "change": {"actions": ["create"], "before": null, "after": {"length": 2},
                "after_unknown": {"id": true}, "before_sensitive": false, "after_sensitive": {}}}
  ]
}`

func TestPreviewLifecycle(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2}, // changes present
		"show -json": {stdout: planJSONChanges},
		"show -no-color": {stdout: `  + resource "random_pet" "name" {
      + length = 2
    }`},
	})
	e := testEngine(fake, schemas.StackDecl{Path: "envs/net", Stacks: []string{"prod"}})

	res, err := e.Preview(context.Background(), testStack, iac.PreviewOpts{Cwd: "/repo/envs/net"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected preview error: %s", res.Error)
	}
	if res.Counts.Add != 1 || res.Counts.Total() != 1 {
		t.Fatalf("counts off: %+v", res.Counts)
	}
	if !strings.Contains(res.PlanSummary, "+ random_pet.name") {
		t.Fatalf("summary missing resource:\n%s", res.PlanSummary)
	}
	if !strings.Contains(res.PlanDiff, `+   resource "random_pet" "name" {`) {
		t.Fatalf("plan diff not formatted:\n%s", res.PlanDiff)
	}
	if !strings.Contains(res.FullPlan, "resource_changes") {
		t.Fatal("full plan JSON not preserved")
	}

	lines := fake.commandLines()
	if len(lines) < 4 {
		t.Fatalf("expected init/select/plan/show sequence, got %v", lines)
	}
	if !strings.HasPrefix(lines[0], "init -input=false -no-color") {
		t.Fatalf("first command must be init, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "workspace select -no-color prod") {
		t.Fatalf("second command must be workspace select, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "-detailed-exitcode") || !strings.Contains(lines[2], "-out=") {
		t.Fatalf("plan must be saved with -detailed-exitcode, got %q", lines[2])
	}
}

func TestPreviewNoChangesExitZero(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 0},
		"show -json": {stdout: `{"format_version":"1.2","resource_changes":[]}`},
	})
	e := testEngine(fake)
	res, err := e.Preview(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/repo/app"})
	if err != nil || res.Error != "" {
		t.Fatalf("exit 0 is success: err=%v resErr=%q", err, res.Error)
	}
	if res.Counts.Total() != 0 {
		t.Fatalf("expected zero counts, got %+v", res.Counts)
	}
	// default workspace: no select call.
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "workspace select") {
			t.Fatalf("default workspace must not be selected: %v", fake.commandLines())
		}
	}
}

func TestPreviewPlanExitOneFails(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan": {exit: 1, stderr: "Error: Invalid provider configuration"},
	})
	e := testEngine(fake)
	res, err := e.Preview(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "Invalid provider configuration") {
		t.Fatalf("plan stderr must surface: %q", res.Error)
	}
}

func TestPreviewInitFailureSurfaced(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"init": {exit: 1, stderr: "Error: Failed to get existing workspaces: S3 bucket does not exist"},
	})
	e := testEngine(fake)
	res, err := e.Preview(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "init failed") || !strings.Contains(res.Error, "S3 bucket does not exist") {
		t.Fatalf("init failure must be clearly surfaced, got %q", res.Error)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("nothing may run after failed init, got %v", fake.commandLines())
	}
}

func TestPreviewUnparseableShowJSONFails(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2},
		"show -json": {stdout: "not json at all"},
	})
	e := testEngine(fake)
	res, err := e.Preview(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("unparseable show -json must fail the preview (fail closed)")
	}
}

func TestWorkspaceCreatedOnlyWhenDeclared(t *testing.T) {
	// Declared stack: select fails -> workspace new runs.
	fake := newFake(t, map[string]fakeResult{
		"workspace select": {exit: 1, stderr: `workspace "prod" doesn't exist`},
		"plan":             {exit: 0},
		"show -json":       {stdout: `{"format_version":"1.2"}`},
	})
	e := testEngine(fake, schemas.StackDecl{Pattern: "envs/*", Stacks: []string{"prod"}})
	res, err := e.Preview(context.Background(), testStack, iac.PreviewOpts{Cwd: "/x"})
	if err != nil || res.Error != "" {
		t.Fatalf("declared workspace should be created: err=%v resErr=%q", err, res.Error)
	}
	created := false
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "workspace new -no-color prod") {
			created = true
		}
	}
	if !created {
		t.Fatalf("workspace new missing: %v", fake.commandLines())
	}

	// Undeclared stack: select fails -> hard error, no creation.
	fake2 := newFake(t, map[string]fakeResult{
		"workspace select": {exit: 1, stderr: `workspace "prod" doesn't exist`},
	})
	e2 := testEngine(fake2) // no declarations
	res2, err := e2.Preview(context.Background(), testStack, iac.PreviewOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Error, "refusing to create") {
		t.Fatalf("undeclared workspace must not be created, got %q", res2.Error)
	}
	for _, line := range fake2.commandLines() {
		if strings.HasPrefix(line, "workspace new") {
			t.Fatalf("workspace new must not run for undeclared stacks: %v", fake2.commandLines())
		}
	}
}

func TestApplyUsesSavedPlan(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2, stdout: "Plan: 1 to add"},
		"show -json": {stdout: planJSONChanges},
		"apply":      {exit: 0, stdout: "Apply complete! Resources: 1 added"},
	})
	e := testEngine(fake, schemas.StackDecl{Path: "envs/net", Stacks: []string{"prod"}})
	res, err := e.Apply(context.Background(), testStack, iac.ApplyOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected apply error: %s", res.Error)
	}
	if res.Counts.Add != 1 {
		t.Fatalf("apply counts off: %+v", res.Counts)
	}
	// The apply must consume the exact -out planfile from the plan step.
	var planOut, applyArg string
	for _, c := range fake.calls {
		joined := strings.Join(c.args, " ")
		if strings.HasPrefix(joined, "plan") {
			for _, a := range c.args {
				if strings.HasPrefix(a, "-out=") {
					planOut = strings.TrimPrefix(a, "-out=")
				}
			}
		}
		if strings.HasPrefix(joined, "apply") {
			applyArg = c.args[len(c.args)-1]
		}
	}
	if planOut == "" || planOut != applyArg {
		t.Fatalf("apply must consume the saved plan file: plan -out=%q vs apply %q", planOut, applyArg)
	}
	if !strings.Contains(res.Output, "Apply complete") {
		t.Fatalf("apply output not captured: %q", res.Output)
	}
}

func TestApplyNoChangesSkipsApply(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan": {exit: 0, stdout: "No changes."},
	})
	e := testEngine(fake)
	res, err := e.Apply(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.ApplyOpts{Cwd: "/x"})
	if err != nil || res.Error != "" {
		t.Fatalf("no-changes apply is success: err=%v resErr=%q", err, res.Error)
	}
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "apply") {
			t.Fatalf("apply must not run when plan reports no changes: %v", fake.commandLines())
		}
	}
}

func TestApplyFailureSurfaced(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan":       {exit: 2},
		"show -json": {stdout: planJSONChanges},
		"apply":      {exit: 1, stderr: "Error: creating resource: AccessDenied\nmore context"},
	})
	e := testEngine(fake)
	res, err := e.Apply(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.ApplyOpts{Cwd: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "AccessDenied") {
		t.Fatalf("apply error must surface stderr, got %q", res.Error)
	}
	if strings.Contains(res.Error, "more context") {
		t.Fatalf("apply error should be first-line only, got %q", res.Error)
	}
}

func TestDriftCheckDetectsDrift(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 2},
		"show -json": {stdout: `{
		  "format_version": "1.2",
		  "resource_drift": [
		    {"address": "aws_s3_bucket.data", "type": "aws_s3_bucket", "name": "data",
		     "change": {"actions": ["update"], "before": {"acl": "private"}, "after": {"acl": "public-read"},
		                "after_unknown": {}, "before_sensitive": {}, "after_sensitive": {}}}
		  ]
		}`},
	})
	e := testEngine(fake, schemas.StackDecl{Path: "envs/net", Stacks: []string{"prod"}})
	res, err := e.DriftCheck(context.Background(), testStack, iac.PreviewOpts{Cwd: "/x"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected drift error: %s", res.Error)
	}
	if res.Counts.Change != 1 || res.Counts.Total() != 1 {
		t.Fatalf("drift counts off: %+v", res.Counts)
	}
	if len(res.DriftedURNs) != 1 || res.DriftedURNs[0] != "aws_s3_bucket.data" {
		t.Fatalf("drifted addresses off: %v", res.DriftedURNs)
	}
	usedRefreshOnly := false
	for _, line := range fake.commandLines() {
		if strings.HasPrefix(line, "plan -refresh-only") && strings.Contains(line, "-detailed-exitcode") {
			usedRefreshOnly = true
		}
	}
	if !usedRefreshOnly {
		t.Fatalf("drift check must use plan -refresh-only -detailed-exitcode: %v", fake.commandLines())
	}
}

func TestDriftCheckNoDrift(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 0},
		"show -json":         {stdout: `{"format_version":"1.2","resource_drift":[]}`},
	})
	e := testEngine(fake)
	res, err := e.DriftCheck(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"}, false)
	if err != nil || res.Error != "" {
		t.Fatalf("clean check is success: err=%v resErr=%q", err, res.Error)
	}
	if res.Counts.Total() != 0 || len(res.DriftedURNs) != 0 {
		t.Fatalf("expected clean result, got %+v %v", res.Counts, res.DriftedURNs)
	}
}

// The false-resolve guard: any check that produces no parseable plan JSON
// must return a non-empty Error AND a non-nil error, exactly like the
// pulumi adapter - the drift runner classifies on this.
func TestDriftCheckFailsClosed(t *testing.T) {
	cases := map[string]map[string]fakeResult{
		"plan exit 1": {
			"plan -refresh-only": {exit: 1, stderr: "Error: state lock timeout"},
		},
		"plan binary missing": {
			"plan -refresh-only": {exit: -1, err: errors.New(`exec: "terraform": executable file not found in $PATH`)},
		},
		"unparseable show json": {
			"plan -refresh-only": {exit: 2},
			"show -json":         {stdout: "garbage"},
		},
		"show fails": {
			"plan -refresh-only": {exit: 2},
			"show -json":         {exit: 1, stderr: "Error: stale plan"},
		},
		"init fails": {
			"init": {exit: 1, stderr: "Error: backend init"},
		},
	}
	for name, results := range cases {
		fake := newFake(t, results)
		e := testEngine(fake)
		res, err := e.DriftCheck(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"}, false)
		if err == nil {
			t.Fatalf("%s: DriftCheck must return a non-nil error (fail closed)", name)
		}
		if res.Error == "" {
			t.Fatalf("%s: DriftCheck must set a non-empty Error (false-resolve guard)", name)
		}
	}
}

func TestDriftCheckJSONAuthoritativeOverExitCode(t *testing.T) {
	// Exit 2 (changes) but the parsed drift set is empty: JSON wins - the
	// verdict is "no drift" because resource_drift is authoritative.
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 2},
		"show -json":         {stdout: `{"format_version":"1.2","resource_drift":[]}`},
	})
	e := testEngine(fake)
	res, err := e.DriftCheck(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"}, false)
	if err != nil || res.Error != "" {
		t.Fatalf("parseable JSON is authoritative: err=%v resErr=%q", err, res.Error)
	}
	if res.Counts.Total() != 0 {
		t.Fatalf("expected no drift from empty drift set, got %+v", res.Counts)
	}
}

func TestDriftCheckMasksSensitiveDrift(t *testing.T) {
	fake := newFake(t, map[string]fakeResult{
		"plan -refresh-only": {exit: 2},
		"show -json": {stdout: `{
		  "format_version": "1.2",
		  "resource_drift": [
		    {"address": "aws_db_instance.db", "type": "aws_db_instance", "name": "db",
		     "change": {"actions": ["update"],
		                "before": {"password": "hunter2"}, "after": {"password": "hunter3"},
		                "after_unknown": {}, "before_sensitive": {"password": true}, "after_sensitive": {"password": true}}}
		  ]
		}`},
	})
	e := testEngine(fake)
	res, err := e.DriftCheck(context.Background(), discovery.Stack{Path: "app", Name: "default"}, iac.PreviewOpts{Cwd: "/x"}, false)
	if err != nil {
		t.Fatal(err)
	}
	combined := res.PlanSummary + res.FullPlan
	if strings.Contains(combined, "hunter2") || strings.Contains(combined, "hunter3") {
		t.Fatalf("sensitive drift values leaked:\n%s", combined)
	}
}
