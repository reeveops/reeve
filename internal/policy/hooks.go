// Package policy runs command-based policy hooks against a plan JSON.
// Engine-agnostic: supports OPA/Conftest/CrossGuard/Sentinel/custom
// scripts. See openspec/specs/iac/policy-hooks.
package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/core/redact"
	"github.com/reeveops/reeve/internal/iac"
)

// Hook is one configured policy hook.
type Hook struct {
	Name    string
	Command []string
	OnFail  FailMode // Block | Warn
	// Required: false = skip silently if command absent. Config defaults
	// this to TRUE when `required:` is omitted (see schemas.PolicyHookYAML
	// - a missing scanner binary fails closed); false is an explicit opt-out.
	Required bool
}

// FailMode controls what happens on a non-zero exit.
type FailMode string

const (
	FailBlock FailMode = "block"
	FailWarn  FailMode = "warn"
)

// Context is the template context passed to the command.
type Context struct {
	PlanJSONPath string
	StackName    string
	Project      string
	Env          string
}

// Result is the outcome of running one hook.
type Result struct {
	Name     string
	Outcome  string // "pass" | "fail" | "warn" | "skipped"
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string
}

// Run executes one hook. Redactor (if non-nil) scrubs stdout/stderr
// before they surface anywhere user-visible.
func Run(ctx context.Context, h Hook, tc Context, r *redact.Redactor) Result {
	// Expand templates.
	args := make([]string, 0, len(h.Command))
	for _, a := range h.Command {
		args = append(args, expand(a, tc))
	}
	if len(args) == 0 {
		return Result{Name: h.Name, Outcome: "skipped", Error: "empty command"}
	}

	// Optional pre-check: does the binary exist?
	if !h.Required {
		if _, err := exec.LookPath(args[0]); err != nil {
			return Result{Name: h.Name, Outcome: "skipped", Error: "command not found (required=false)"}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// #nosec G204 -- DELIBERATE: a policy hook IS an operator-supplied command from engine config.
	// It is argv-form with no shell and reached only after independent apply
	// gates pass. Its environment excludes controller and apply credentials.
	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	childEnv, cleanup, envErr := iac.CommandEnv(nil, nil)
	if envErr != nil {
		return failedResult(h, fmt.Errorf("prepare child environment: %w", envErr))
	}
	defer cleanup()
	cmd.Env = childEnv
	runErr := cmd.Run()

	out := r.Redact(stdout.String())
	errOut := r.Redact(stderr.String())
	res := Result{
		Name:   h.Name,
		Stdout: out,
		Stderr: errOut,
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	res.ExitCode = exitCode

	if exitCode == 0 {
		res.Outcome = "pass"
		return res
	}
	if h.OnFail == FailWarn {
		res.Outcome = "warn"
	} else {
		res.Outcome = "fail"
	}
	if runErr != nil {
		res.Error = firstLine(runErr.Error())
	}
	return res
}

func failedResult(h Hook, err error) Result {
	outcome := "fail"
	if h.OnFail == FailWarn {
		outcome = "warn"
	}
	return Result{Name: h.Name, Outcome: outcome, ExitCode: -1, Error: firstLine(err.Error())}
}

// Aggregate tells preconditions whether any blocking hooks failed.
// Returns (passed, results). passed == true means all block-mode hooks
// either passed or were skipped.
func Aggregate(results []Result) (bool, []Result) {
	passed := true
	for _, r := range results {
		if r.Outcome == "fail" {
			passed = false
		}
	}
	return passed, results
}

// expand replaces {{plan_json}}, {{stack_name}}, {{project}}, {{env}}.
func expand(s string, c Context) string {
	s = strings.ReplaceAll(s, "{{plan_json}}", c.PlanJSONPath)
	s = strings.ReplaceAll(s, "{{stack_name}}", c.StackName)
	s = strings.ReplaceAll(s, "{{project}}", c.Project)
	s = strings.ReplaceAll(s, "{{env}}", c.Env)
	return s
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		return s[:idx]
	}
	return s
}

// RenderSection returns a markdown section summarizing hook results for
// inclusion in the PR comment.
func RenderSection(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n🔐 Policy:\n")
	for _, r := range results {
		icon := "✅"
		switch r.Outcome {
		case "fail":
			icon = "❌"
		case "warn":
			icon = "⚠️"
		case "skipped":
			icon = "⏸"
		}
		fmt.Fprintf(&b, "  %s %s", icon, r.Name)
		if r.Error != "" {
			fmt.Fprintf(&b, ": %s", r.Error)
		}
		b.WriteString("\n")
		if (r.Outcome == "fail" || r.Outcome == "warn") && r.Stdout != "" {
			fmt.Fprintf(&b, "    ```\n%s\n    ```\n", trim(r.Stdout, 2000))
		}
	}
	return b.String()
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}
