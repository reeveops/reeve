package hcl

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// DriftCheck runs `plan -refresh-only -detailed-exitcode` and parses the
// saved plan's `show -json` output. Exit 2 means state and reality differ
// (drift); the drifted resources come from resource_drift. A refresh-only
// plan reads live infrastructure without mutating state, so refreshFirst
// needs no separate refresh step - the check itself is always fresh (and
// reeve never writes engine state during a drift check).
//
// Fail-closed contract (mirrors the pulumi adapter exactly - the drift
// runner depends on it): parseable plan JSON is authoritative for the
// drift verdict regardless of exit code; a check that produces no
// parseable JSON returns a non-empty Error AND a non-nil error, so the
// caller records a failed check instead of misreading an empty result as
// "no drift" (which would falsely resolve an active drift alert).
func (e *Engine) DriftCheck(ctx context.Context, stack discovery.Stack, opts iac.PreviewOpts, refreshFirst bool) (iac.PreviewResult, error) {
	_ = refreshFirst // refresh-only plans always evaluate live infra; see above.

	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	runCtx, cancel := context.WithTimeout(ctx, opTimeout(opts.TimeoutSec, 10*time.Minute))
	defer cancel()

	failClosed := func(msg, fullPlan string) (iac.PreviewResult, error) {
		return iac.PreviewResult{Error: msg, FullPlan: fullPlan},
			fmt.Errorf("%s drift check produced no plan: %s", e.dialect.Display, msg)
	}

	if res, err := e.tfInit(runCtx, cwd, opts.Env); err != nil {
		return failClosed(err.Error(), string(res.Stderr)+string(res.Stdout))
	}
	if err := e.selectWorkspace(runCtx, cwd, opts.Env, stack); err != nil {
		return failClosed(err.Error(), "")
	}

	planPath, err := e.planFile()
	if err != nil {
		return failClosed("create plan file: "+err.Error(), "")
	}
	defer os.Remove(planPath)

	args := []string{"plan", "-refresh-only", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + planPath}
	args = append(args, opts.ExtraArgs...)
	plan, runErr := e.run(runCtx, cwd, opts.Env, e.Binary, args...)
	if runErr != nil || (plan.ExitCode != exitNoChanges && plan.ExitCode != exitChanges) {
		return failClosed(
			e.dialect.Display+" refresh-only plan failed: "+failureMessage(string(plan.Stderr), runErr),
			string(plan.Stderr)+string(plan.Stdout))
	}

	show, showErr := e.run(runCtx, cwd, opts.Env, e.Binary, "show", "-json", planPath)
	if showErr != nil || show.ExitCode != 0 {
		return failClosed(
			e.dialect.Display+" show -json failed: "+failureMessage(string(show.Stderr), showErr),
			string(show.Stderr))
	}
	p, perr := parsePlan(show.Stdout)
	if perr != nil {
		// Unparseable JSON: no verdict. Never treat as "no drift".
		return failClosed(perr.Error(), string(show.Stderr))
	}

	full, ferr := scrubPlanJSON(show.Stdout)
	if ferr != nil {
		full = "" // never store the unscrubbed blob
	}
	return iac.PreviewResult{
		Counts:      countsFrom(p.ResourceDrift),
		PlanSummary: shortSummary(p.ResourceDrift, 10),
		FullPlan:    full,
		// Drifted addresses play the role Pulumi URNs do: the drift runner
		// fingerprints this set so a change in WHICH resources drift
		// re-fires the alert.
		DriftedURNs: changedAddresses(p.ResourceDrift),
		Resources:   driftResources(p.ResourceDrift),
	}, nil
}
