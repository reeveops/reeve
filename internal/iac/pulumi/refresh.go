package pulumi

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// Refresh runs `pulumi refresh` for one stack, reconciling stack state with
// what the providers actually report. It changes no infrastructure; it
// rewrites state, so callers hold the stack lock across the call.
//
// PreviewOnly maps to `--preview-only`, which computes the same
// reconciliation and exits without writing state. `--yes` is omitted there:
// there is nothing to confirm, and passing an approval flag to a read-only
// command invites the exact mistake this mode exists to avoid.
//
// Counts come from the same summary event `pulumi up` emits, so a "delete"
// means "the resource is gone from the cloud and was dropped from state" -
// never "reeve destroyed it".
func (e *Engine) Refresh(ctx context.Context, stack discovery.Stack, opts iac.RefreshOpts) (iac.RefreshResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}

	args := []string{"refresh", "--stack", stack.Name, "--non-interactive", "--json"}
	if opts.PreviewOnly {
		args = append(args, "--preview-only")
	} else {
		args = append(args, "--yes")
	}
	args = append(args, opts.ExtraArgs...)

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	// #nosec G204 -- e.Binary is engine.binary.path from operator config; args are built by this
	// adapter and passed as argv with no shell
	cmd := exec.CommandContext(runCtx, e.Binary, args...)
	iac.SetupGracefulStop(cmd, 0)
	cmd.Dir = cwd
	childEnv, cleanup, envErr := commandEnv(opts.Env, false)
	if envErr != nil {
		return iac.RefreshResult{}, envErr
	}
	defer cleanup()
	cmd.Env = childEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := iac.RefreshResult{
		Output:     stderr.String() + stdout.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}
	counts, diagErr := parseApply(stdout.Bytes())
	res.Counts = counts

	// Same rule as apply: engine diagnostics carry the cause, stderr is the
	// fallback, and nothing is truncated to one line.
	switch {
	case diagErr != "":
		res.Error = diagErr
		if runErr != nil {
			if extra := strings.TrimSpace(stderr.String()); extra != "" {
				res.Error += "\n" + extra
			}
		}
	case runErr != nil:
		res.Error = failureMessage(stderr.String(), runErr)
	}
	return res, nil
}
