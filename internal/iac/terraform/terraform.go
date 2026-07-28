// Package terraform implements the iac.Engine contract for Terraform and
// OpenTofu via CLI shell-out. One parameterized adapter serves both: the
// Variant selects the binary name, display name, and registry key
// (engine.type "terraform" / "tofu").
//
// Stack model: a root-module directory is a project; a `terraform workspace`
// is a stack. Dir-per-env layouts enumerate as <project>/default. The
// lifecycle per stack is init → workspace select → plan/apply/refresh-only
// plan, with `terraform show -json` output as the authoritative plan record.
package terraform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac"
)

// Variant selects which CLI flavor the adapter drives.
type Variant struct {
	TypeName string // registry key / config engine.type
	Display  string // human display name (iac.Engine.Name)
	Binary   string // default binary name when engine.binary.path is unset
}

// The two supported variants. OpenTofu is CLI-compatible with Terraform for
// everything this adapter uses (init, workspace, plan -detailed-exitcode,
// show -json, apply <planfile>).
var (
	Terraform = Variant{TypeName: "terraform", Display: "Terraform", Binary: "terraform"}
	OpenTofu  = Variant{TypeName: "tofu", Display: "OpenTofu", Binary: "tofu"}
)

// init self-registers both engine types; blank-importing this package
// (internal/iac/all does for the default set) is what compiles them in.
func init() {
	iac.Register(Terraform.TypeName, func(cfg schemas.EngineBody) (iac.Engine, error) {
		return New(Terraform, cfg), nil
	})
	iac.Register(OpenTofu.TypeName, func(cfg schemas.EngineBody) (iac.Engine, error) {
		return New(OpenTofu, cfg), nil
	})
}

// Engine is the Terraform/OpenTofu iac.Engine adapter.
type Engine struct {
	Binary  string // binary path (default: variant binary name)
	variant Variant
	// decls are the engine config's declared stacks. When present they are
	// authoritative for enumeration (no `workspace list` needed) and gate
	// workspace creation: a declared-but-missing workspace is created on
	// select; an undeclared one never is.
	decls []discovery.Declaration
	// run executes one CLI command. Overridable so tests fake the binary.
	run runCmd
}

// New returns an Engine for the given variant, honoring
// engine.binary.path overrides from config.
func New(v Variant, cfg schemas.EngineBody) *Engine {
	bin := cfg.Binary.Path
	if bin == "" {
		bin = v.Binary
	}
	decls := make([]discovery.Declaration, 0, len(cfg.Stacks))
	for _, s := range cfg.Stacks {
		decls = append(decls, discovery.Declaration{
			Project: s.Project, Path: s.Path, Pattern: s.Pattern, Stacks: s.Stacks,
		})
	}
	return &Engine{Binary: bin, variant: v, decls: decls, run: realRun}
}

func (e *Engine) Name() string { return e.variant.Display }

func (e *Engine) Capabilities() iac.Capabilities {
	return iac.Capabilities{
		// Apply consumes the exact saved plan file produced by its own
		// plan step (plan-what-you-apply parity).
		SupportsSavedPlans: true,
		// Drift checks run `plan -refresh-only`, which always evaluates
		// live infrastructure without mutating state.
		SupportsRefresh:      true,
		SupportsPolicyNative: false,
		// State encryption is a backend concern (S3 SSE, etc.) - there is
		// no engine-side secrets provider to configure.
		SecretsProviderTypes: nil,
	}
}

// execResult carries one CLI command's outcome through the runner seam.
type execResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// runCmd executes the engine binary in dir with extra env. A non-zero exit
// from a command that ran to completion is NOT an error (the caller
// classifies exit codes); err is non-nil only when the command could not
// run at all (missing binary, killed by context timeout).
type runCmd func(ctx context.Context, dir string, env map[string]string, bin string, args ...string) (execResult, error)

// realRun is the production runCmd: os/exec with combined env.
func realRun(ctx context.Context, dir string, env map[string]string, bin string, args ...string) (execResult, error) {
	// #nosec G204 -- bin is engine.binary.path from operator config (default `terraform`/`tofu`);
	// args are built by this adapter, passed as argv with no shell, so no
	// metacharacter injection
	cmd := exec.CommandContext(ctx, bin, args...)
	iac.SetupGracefulStop(cmd, 0)
	cmd.Dir = dir
	// TF_IN_AUTOMATION suppresses interactive-use hints in CLI output.
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := execResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		// The context deadline kills the process, which surfaces as an
		// ExitError; report it as a hard failure, not an exit code.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), ctxErr)
		}
		return res, nil
	}
	if err != nil {
		res.ExitCode = -1
		return res, err
	}
	return res, nil
}

// opTimeout resolves the per-operation timeout.
func opTimeout(sec int, fallback time.Duration) time.Duration {
	if sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return fallback
}

// tfInit runs `init -input=false -no-color`. Init failures are the #1 UX
// failure mode (backend auth, provider mirror, version constraints), so the
// message always carries the init prefix plus the CLI's own stderr.
func (e *Engine) tfInit(ctx context.Context, cwd string, env map[string]string) (execResult, error) {
	res, err := e.run(ctx, cwd, env, e.Binary, "init", "-input=false", "-no-color")
	if err != nil {
		return res, fmt.Errorf("%s init failed: %s", e.variant.Display, failureMessage(string(res.Stderr), err))
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s init failed: %s", e.variant.Display, failureMessage(string(res.Stderr), nil))
	}
	return res, nil
}

// selectWorkspace switches to the stack's workspace. The "default"
// workspace always exists and needs no select. A missing workspace is
// created only when the stack is declared in engine config - reeve never
// invents workspaces on its own.
func (e *Engine) selectWorkspace(ctx context.Context, cwd string, env map[string]string, stack discovery.Stack) error {
	if stack.Name == "" || stack.Name == defaultWorkspace {
		return nil
	}
	res, err := e.run(ctx, cwd, env, e.Binary, "workspace", "select", "-no-color", stack.Name)
	if err != nil {
		return fmt.Errorf("%s workspace select %s failed: %s", e.variant.Display, stack.Name, failureMessage(string(res.Stderr), err))
	}
	if res.ExitCode == 0 {
		return nil
	}
	if !e.stackDeclared(stack) {
		return fmt.Errorf("%s workspace select %s failed (workspace not declared in engine config, refusing to create it): %s",
			e.variant.Display, stack.Name, failureMessage(string(res.Stderr), nil))
	}
	newRes, err := e.run(ctx, cwd, env, e.Binary, "workspace", "new", "-no-color", stack.Name)
	if err != nil || newRes.ExitCode != 0 {
		return fmt.Errorf("%s workspace new %s failed: %s", e.variant.Display, stack.Name, failureMessage(string(newRes.Stderr), err))
	}
	return nil
}

// stackDeclared reports whether engine config declares this (path, stack)
// pair, via a literal entry or a doublestar pattern.
func (e *Engine) stackDeclared(stack discovery.Stack) bool {
	for _, d := range e.decls {
		if !containsName(d.Stacks, stack.Name) {
			continue
		}
		if d.Path != "" && d.Path == stack.Path {
			return true
		}
		if d.Pattern != "" {
			if ok, _ := doublestar.Match(d.Pattern, stack.Path); ok {
				return true
			}
		}
	}
	return false
}

func containsName(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// planFile creates the temp file a saved plan is written to. Callers must
// os.Remove it.
func (e *Engine) planFile() (string, error) {
	f, err := os.CreateTemp("", "reeve-"+e.variant.TypeName+"-plan-*.tfplan")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_ = f.Close()
	return name, nil
}

// failureMessage builds a non-empty error string from stderr, falling back
// to the process error. Never returns "" (drift's fail-closed contract
// depends on a non-empty Error).
func failureMessage(stderr string, err error) string {
	stderr = strings.TrimSpace(stderr)
	switch {
	case stderr != "" && err != nil:
		return stderr + " (" + err.Error() + ")"
	case stderr != "":
		return stderr
	case err != nil:
		return err.Error()
	default:
		return "command failed with no output"
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		return s[:idx]
	}
	return s
}

// formatDiff moves +/-/~ from after indentation to line start so GitHub's
// diff code fence colors them (same transform as the pulumi adapter).
func formatDiff(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) == 0 {
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		switch trimmed[0] {
		case '+':
			lines[i] = "+" + indent + trimmed[1:]
		case '-':
			lines[i] = "-" + indent + trimmed[1:]
		case '~':
			lines[i] = "!" + indent + trimmed[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// compile-time check: the adapter satisfies the full engine contract.
var _ iac.Engine = (*Engine)(nil)
