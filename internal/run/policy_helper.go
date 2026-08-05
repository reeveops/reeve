package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/policy"
)

// HooksFromEngine converts engine config into policy.Hook list.
func HooksFromEngine(e *schemas.Engine) []policy.Hook {
	if e == nil {
		return nil
	}
	out := make([]policy.Hook, 0, len(e.Engine.PolicyHooks))
	for _, h := range e.Engine.PolicyHooks {
		mode := policy.FailBlock
		if h.OnFail == "warn" {
			mode = policy.FailWarn
		}
		out = append(out, policy.Hook{
			Name: h.Name, Command: h.Command,
			// Omitted `required:` defaults to TRUE (fail closed): a hook
			// whose scanner binary is missing fails the run instead of
			// silently skipping the policy gate.
			OnFail: mode, Required: h.IsRequired(),
		})
	}
	return out
}

// RunPolicyForStack executes all hooks for one stack. Writes the plan
// JSON to a temp file first and supplies its path via {{plan_json}}.
// Returns (passed, results) - passed is true iff all block hooks passed.
func RunPolicyForStack(ctx context.Context, hooks []policy.Hook, s discovery.Stack, ss summary.StackSummary, r *redact.Redactor) (bool, []policy.Result, error) {
	if len(hooks) == 0 {
		return true, nil, nil
	}
	planPath, planDir, err := writePlanJSON(ss)
	if err != nil {
		return false, nil, err
	}
	defer os.RemoveAll(planDir)

	tc := policy.Context{
		PlanJSONPath: planPath,
		StackName:    s.Name,
		Project:      s.Project,
		Env:          s.Env,
	}
	results := make([]policy.Result, 0, len(hooks))
	passed := true
	for _, h := range hooks {
		res := policy.Run(ctx, h, tc, r)
		results = append(results, res)
		if res.Outcome == "fail" {
			passed = false
		}
	}
	return passed, results, nil
}

// writePlanJSON dumps both the structured engine plan (parsed from
// FullPlan when it's JSON) and reeve's summary metadata to a temp file.
// Policy systems can inspect either: `.plan` carries the raw engine
// output (full resource bodies, URNs, steps), while `.counts` and
// `.plan_summary` carry the human summary.
//
// When FullPlan is non-JSON (e.g. an error string), `.plan` is emitted
// as a string so consumers can still pattern-match against it.
func writePlanJSON(ss summary.StackSummary) (path, dir string, err error) {
	var planField any
	trimmed := strings.TrimSpace(ss.FullPlan)
	if trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		var raw any
		if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
			planField = raw
		}
	}
	if planField == nil && ss.FullPlan != "" {
		planField = ss.FullPlan
	}

	payload := map[string]any{
		"project": ss.Project,
		"stack":   ss.Stack,
		"env":     ss.Env,
		"counts": map[string]int{
			"add":     ss.Counts.Add,
			"change":  ss.Counts.Change,
			"delete":  ss.Counts.Delete,
			"replace": ss.Counts.Replace,
		},
		"plan_summary": ss.PlanSummary,
		"plan":         planField,
	}
	data, merr := json.MarshalIndent(payload, "", "  ")
	if merr != nil {
		return "", "", merr
	}
	dir, merr = os.MkdirTemp("", "reeve-plan-")
	if merr != nil {
		return "", "", merr
	}
	p := filepath.Join(dir, fmt.Sprintf("%s-%s.json", ss.Project, ss.Stack))
	if merr = os.WriteFile(p, data, 0o600); merr != nil {
		_ = os.RemoveAll(dir)
		return "", "", merr
	}
	return p, dir, nil
}
