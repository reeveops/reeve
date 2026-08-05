package hcl

import (
	"context"
	"os"
	"time"

	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// Apply runs the saved-plan lifecycle for one stack:
//
//	init → workspace select → plan -detailed-exitcode -out=<file>
//	→ apply -input=false <file>
//
// The apply always consumes a plan file rather than deciding for itself
// (SupportsSavedPlans). Which plan depends on the caller:
//
//   - ApplyOpts.PlanPath set (plan locking on): that artifact is applied
//     verbatim and NO plan runs here. The CLI itself refuses a plan whose
//     state lineage or serial has moved on, so an apply raced by another
//     merge fails instead of quietly shipping a different change set.
//   - PlanPath empty: a plan is computed here and applied immediately. The
//     executed change set matches what this call just planned - but not
//     necessarily what the PR previewed, which is exactly the "last apply
//     wins" gap plan locking closes.
//
// A plan with no changes (exit 0) skips the apply entirely.
func (e *Engine) Apply(ctx context.Context, stack discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	runCtx, cancel := context.WithTimeout(ctx, opTimeout(opts.TimeoutSec, 30*time.Minute))
	defer cancel()

	start := time.Now()
	fail := func(msg, output string) (iac.ApplyResult, error) {
		return iac.ApplyResult{
			Error:      firstLine(msg),
			Output:     output,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil
	}

	// A refresh recomputes the diff, which is the one thing a locked plan
	// forbids. Refuse the combination rather than silently dropping one.
	if opts.PlanPath != "" && opts.Refresh {
		return fail("refresh cannot be combined with a locked plan: a refresh would change the diff the saved plan pins", "")
	}

	if res, err := e.tfInit(runCtx, cwd, opts.Env); err != nil {
		return fail(err.Error(), string(res.Stderr)+string(res.Stdout))
	}
	if err := e.selectWorkspace(runCtx, cwd, opts.Env, stack); err != nil {
		return fail(err.Error(), "")
	}

	planPath := opts.PlanPath
	planOut := ""
	if planPath == "" {
		p, err := e.planFile()
		if err != nil {
			return fail("create plan file: "+err.Error(), "")
		}
		planPath = p
		defer os.Remove(planPath)

		args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + planPath}
		if opts.Refresh {
			args = append(args, "-refresh=true")
		}
		args = append(args, opts.ExtraArgs...)
		plan, runErr := e.run(runCtx, cwd, opts.Env, e.Binary, args...)
		planOut = string(plan.Stderr) + string(plan.Stdout)
		if runErr != nil || (plan.ExitCode != exitNoChanges && plan.ExitCode != exitChanges) {
			return fail(e.dialect.Display+" plan failed: "+failureMessage(string(plan.Stderr), runErr), planOut)
		}
		if plan.ExitCode == exitNoChanges {
			return iac.ApplyResult{
				Output:     planOut,
				DurationMS: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Counts come from the saved plan (what WILL be applied) - parsed
	// before apply so a mid-apply failure still reports the intended set.
	var counts summary.Counts
	if parsed, perr := e.readPlan(runCtx, cwd, opts.Env, planPath); perr == nil {
		counts = parsed.Counts
	}

	apply, applyErr := e.run(runCtx, cwd, opts.Env, e.Binary, "apply", "-input=false", "-no-color", planPath)
	output := planOut + string(apply.Stderr) + string(apply.Stdout)
	result := iac.ApplyResult{
		Counts:     counts,
		Output:     output,
		DurationMS: time.Since(start).Milliseconds(),
	}
	if applyErr != nil || apply.ExitCode != 0 {
		result.Error = firstLine(failureMessage(string(apply.Stderr), applyErr))
	}
	return result, nil
}
