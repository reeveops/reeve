// Package hcl is the shared engine for the HCL-based IaC CLIs: Terraform and
// OpenTofu. Both drive the same command sequence - init → workspace select →
// plan -detailed-exitcode -out → show -json → apply <planfile>, plus
// plan -refresh-only for drift - and both emit the same plan JSON, so the
// lifecycle, the plan parser and root-module discovery live here once.
//
// Where they differ, they differ through a Dialect. Each engine ships its own
// package (internal/iac/terraform, internal/iac/tofu) contributing only its
// Dialect and its registry entry, so a new divergence has an obvious home
// instead of becoming a conditional buried in shared code. The trigger for
// splitting this package for real is written down in
// openspec/specs/iac/spec.md.
//
// Stack model: a root-module directory is a project; a workspace is a stack.
// Dir-per-env layouts enumerate as <project>/default.
package hcl

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

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// Dialect declares how one HCL engine differs from the others. It is data
// today because every current difference is data. When a difference is
// behavioral, add a method here and have the shared code call it - that is
// cheaper and more visible than a conditional in the lifecycle.
type Dialect struct {
	// TypeName is the registry key and the config engine.type value.
	TypeName string
	// Display is the human name reported by iac.Engine.Name.
	Display string
	// Binary is the default executable when engine.binary.path is unset.
	Binary string
	// SourceExts are the file extensions that mark a root module for this
	// engine. Terraform reads only .tf; OpenTofu also reads .tofu, and where
	// a base name exists as both, .tofu wins - so a repo written for
	// OpenTofu can contain no .tf at all.
	SourceExts []string
	// Caps is what this engine can do. It is per-dialect rather than shared
	// because the engines genuinely differ: OpenTofu configures state
	// encryption in the language, Terraform leaves it to the backend.
	Caps iac.Capabilities
}

// Engine is the shared HCL adapter, bound to one Dialect.
type Engine struct {
	Binary  string // binary path (default: the dialect's binary name)
	dialect Dialect
	// decls are the engine config's declared stacks. When present they are
	// authoritative for enumeration (no `workspace list` needed) and gate
	// workspace creation: a declared-but-missing workspace is created on
	// select; an undeclared one never is.
	decls []discovery.Declaration
	// run executes one CLI command. Overridable so tests fake the binary.
	run runCmd
}

// New returns an Engine for the dialect, honoring engine.binary.path.
func New(d Dialect, cfg schemas.EngineBody) *Engine {
	bin := cfg.Binary.Path
	if bin == "" {
		bin = d.Binary
	}
	decls := make([]discovery.Declaration, 0, len(cfg.Stacks))
	for _, s := range cfg.Stacks {
		decls = append(decls, discovery.Declaration{
			Project: s.Project, Path: s.Path, Pattern: s.Pattern, Stacks: s.Stacks,
		})
	}
	return &Engine{Binary: bin, dialect: d, decls: decls, run: realRun}
}

// Constructor returns an iac.Constructor for the dialect. Each engine package
// passes this to iac.Register from its init(), so registration keys stay
// package-local while the implementation stays here.
func Constructor(d Dialect) func(schemas.EngineBody) (iac.Engine, error) {
	return func(cfg schemas.EngineBody) (iac.Engine, error) { return New(d, cfg), nil }
}

// Dialect returns the engine's dialect. Exposed for tests and for engine
// packages that need to assert what they registered.
func (e *Engine) Dialect() Dialect { return e.dialect }

func (e *Engine) Name() string { return e.dialect.Display }

func (e *Engine) Capabilities() iac.Capabilities { return e.dialect.Caps }

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
	childEnv, cleanup, err := iac.CommandEnv(env, map[string]string{"TF_IN_AUTOMATION": "1"})
	if err != nil {
		return execResult{ExitCode: -1}, fmt.Errorf("prepare child environment: %w", err)
	}
	defer cleanup()
	cmd.Env = childEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
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
		return res, fmt.Errorf("%s init failed: %s", e.dialect.Display, failureMessage(string(res.Stderr), err))
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s init failed: %s", e.dialect.Display, failureMessage(string(res.Stderr), nil))
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
		return fmt.Errorf("%s workspace select %s failed: %s", e.dialect.Display, stack.Name, failureMessage(string(res.Stderr), err))
	}
	if res.ExitCode == 0 {
		return nil
	}
	if !e.stackDeclared(stack) {
		return fmt.Errorf("%s workspace select %s failed (workspace not declared in engine config, refusing to create it): %s",
			e.dialect.Display, stack.Name, failureMessage(string(res.Stderr), nil))
	}
	newRes, err := e.run(ctx, cwd, env, e.Binary, "workspace", "new", "-no-color", stack.Name)
	if err != nil || newRes.ExitCode != 0 {
		return fmt.Errorf("%s workspace new %s failed: %s", e.dialect.Display, stack.Name, failureMessage(string(newRes.Stderr), err))
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
	f, err := os.CreateTemp("", "reeve-"+e.dialect.TypeName+"-plan-*.tfplan")
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
