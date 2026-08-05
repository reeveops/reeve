package run

import (
	"context"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/policy"
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
