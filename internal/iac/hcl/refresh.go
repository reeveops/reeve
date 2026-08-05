package hcl

import (
	"context"
	"os"
	"time"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// Refresh reconciles state with live infrastructure for one stack:
//
//	init → workspace select → plan -refresh-only -detailed-exitcode -out=<f>
//	→ show -json <f> (counts)  → apply <f>   (skipped when PreviewOnly)
//
// The refresh-only plan is what makes the read-only mode honest: it reads
// the providers and writes nothing, so PreviewOnly stops after `show -json`
// with real counts and an untouched state file. Only the trailing `apply`
// commits the reconciliation.
//
// Counts come from the plan's resource_drift block - the same source the
// drift check uses - so they describe state reconciliation, never
// infrastructure change.
func (e *Engine) Refresh(ctx context.Context, stack discovery.Stack, opts iac.RefreshOpts) (iac.RefreshResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	runCtx, cancel := context.WithTimeout(ctx, opTimeout(opts.TimeoutSec, 30*time.Minute))
	defer cancel()

	start := time.Now()
	fail := func(msg, output string) (iac.RefreshResult, error) {
		return iac.RefreshResult{
			Error:      firstLine(msg),
			Output:     output,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil
	}

	if res, err := e.tfInit(runCtx, cwd, opts.Env); err != nil {
		return fail(err.Error(), string(res.Stderr)+string(res.Stdout))
	}
	if err := e.selectWorkspace(runCtx, cwd, opts.Env, stack); err != nil {
		return fail(err.Error(), "")
	}

	planPath, err := e.planFile()
	if err != nil {
		return fail("create plan file: "+err.Error(), "")
	}
	defer os.Remove(planPath)

	args := []string{"plan", "-refresh-only", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + planPath}
	args = append(args, opts.ExtraArgs...)
	plan, runErr := e.run(runCtx, cwd, opts.Env, e.Binary, args...)
	out := string(plan.Stderr) + string(plan.Stdout)
	if runErr != nil || (plan.ExitCode != exitNoChanges && plan.ExitCode != exitChanges) {
		return fail(e.dialect.Display+" refresh-only plan failed: "+failureMessage(string(plan.Stderr), runErr), out)
	}

	res := iac.RefreshResult{Output: out}
	if show, showErr := e.run(runCtx, cwd, opts.Env, e.Binary, "show", "-json", planPath); showErr == nil && show.ExitCode == 0 {
		if p, perr := parsePlan(show.Stdout); perr == nil {
			res.Counts = countsFrom(p.ResourceDrift)
			res.Summary = shortSummary(p.ResourceDrift, 10)
		}
	}

	if opts.PreviewOnly {
		res.DurationMS = time.Since(start).Milliseconds()
		return res, nil
	}

	// Nothing drifted: committing an empty refresh-only plan is a no-op, so
	// skip the state write entirely.
	if plan.ExitCode == exitNoChanges {
		res.DurationMS = time.Since(start).Milliseconds()
		return res, nil
	}

	apply, applyErr := e.run(runCtx, cwd, opts.Env, e.Binary, "apply", "-input=false", "-no-color", planPath)
	res.Output = out + string(apply.Stderr) + string(apply.Stdout)
	res.DurationMS = time.Since(start).Milliseconds()
	if applyErr != nil || apply.ExitCode != 0 {
		res.Error = firstLine(failureMessage(string(apply.Stderr), applyErr))
	}
	return res, nil
}
