package hcl

import (
	"context"
	"os"
	"time"

	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac"
)

// Plan exit codes under -detailed-exitcode.
const (
	exitNoChanges = 0
	exitError     = 1
	exitChanges   = 2
)

// Preview runs the full plan lifecycle for one stack:
//
//	init -input=false → workspace select → plan -detailed-exitcode -out=<file>
//	→ show -json <file> (parsed into PreviewResult)
//
// Exit codes: 0 = no changes, 2 = changes, anything else = failure. The
// parsed show -json output is authoritative for counts and diffs; sensitive
// values are masked before anything is stored. CLI failures populate
// PreviewResult.Error (nil error), matching the pulumi adapter's shape.
func (e *Engine) Preview(ctx context.Context, stack discovery.Stack, opts iac.PreviewOpts) (iac.PreviewResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	runCtx, cancel := context.WithTimeout(ctx, opTimeout(opts.TimeoutSec, 10*time.Minute))
	defer cancel()

	if res, err := e.tfInit(runCtx, cwd, opts.Env); err != nil {
		return iac.PreviewResult{Error: err.Error(), FullPlan: string(res.Stderr) + string(res.Stdout)}, nil
	}
	if err := e.selectWorkspace(runCtx, cwd, opts.Env, stack); err != nil {
		return iac.PreviewResult{Error: err.Error()}, nil
	}

	planPath, err := e.planFile()
	if err != nil {
		return iac.PreviewResult{Error: "create plan file: " + err.Error()}, nil
	}
	defer os.Remove(planPath)

	args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + planPath}
	args = append(args, opts.ExtraArgs...)
	plan, runErr := e.run(runCtx, cwd, opts.Env, e.Binary, args...)
	if runErr != nil || (plan.ExitCode != exitNoChanges && plan.ExitCode != exitChanges) {
		msg := e.dialect.Display + " plan failed: " + failureMessage(string(plan.Stderr), runErr)
		return iac.PreviewResult{Error: msg, FullPlan: string(plan.Stderr) + string(plan.Stdout)}, nil
	}

	result, perr := e.readPlan(runCtx, cwd, opts.Env, planPath)
	if perr != nil {
		return iac.PreviewResult{
			Error:    e.dialect.Display + " show -json failed: " + perr.Error(),
			FullPlan: string(plan.Stderr) + string(plan.Stdout),
		}, nil
	}
	result.PlanDiff = e.humanPlan(runCtx, cwd, opts.Env, planPath)
	return result, nil
}

// readPlan converts a saved plan file into a PreviewResult via
// `show -json`. The scrubbed plan JSON (sensitive values masked) becomes
// FullPlan; the raw blob is never stored.
func (e *Engine) readPlan(ctx context.Context, cwd string, env map[string]string, planPath string) (iac.PreviewResult, error) {
	show, err := e.run(ctx, cwd, env, e.Binary, "show", "-json", planPath)
	if err != nil {
		return iac.PreviewResult{}, err
	}
	if show.ExitCode != 0 {
		return iac.PreviewResult{}, errFrom(failureMessage(string(show.Stderr), nil))
	}
	p, err := parsePlan(show.Stdout)
	if err != nil {
		return iac.PreviewResult{}, err
	}
	full, err := scrubPlanJSON(show.Stdout)
	if err != nil {
		// Never fall back to the unscrubbed blob - drop it instead.
		full = ""
	}
	return iac.PreviewResult{
		Counts:      countsFrom(p.ResourceChanges),
		PlanSummary: shortSummary(p.ResourceChanges, 10),
		FullPlan:    full,
	}, nil
}

// humanPlan renders `show -no-color <planfile>` for the human-readable
// diff. Best-effort: errors yield an empty string. Terraform masks
// sensitive values in this output itself ("(sensitive value)").
func (e *Engine) humanPlan(ctx context.Context, cwd string, env map[string]string, planPath string) string {
	res, err := e.run(ctx, cwd, env, e.Binary, "show", "-no-color", planPath)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return formatDiff(string(res.Stdout))
}

// errFrom wraps a message string as an error (keeps call sites terse).
type errString string

func (e errString) Error() string { return string(e) }

func errFrom(msg string) error { return errString(msg) }
