package pulumi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

// init self-registers the pulumi engine; blank-importing this package
// (internal/iac/all does for the default set) is what compiles it in.
func init() {
	iac.Register("pulumi", func(cfg schemas.EngineBody) (iac.Engine, error) {
		return New(cfg.Binary.Path), nil
	})
}

// Engine is the Pulumi iac.Engine adapter. Shells out to the pulumi CLI.
type Engine struct {
	Binary string // path to pulumi binary (default: "pulumi")
}

// New returns an Engine with defaults.
func New(binary string) *Engine {
	if binary == "" {
		binary = "pulumi"
	}
	return &Engine{Binary: binary}
}

func (e *Engine) Name() string { return "pulumi" }

func (e *Engine) Capabilities() iac.Capabilities {
	return iac.Capabilities{
		// `preview --save-plan` + `up --plan` (update plans). Pulumi still
		// tags both flags experimental and hides them unless
		// PULUMI_EXPERIMENTAL=true, which this adapter sets on exactly the
		// invocations that pass them (see experimentalEnv). Capability is
		// declared true because the lifecycle is wired; operators who do not
		// want to ride an experimental Pulumi flag turn plan locking off in
		// engine config.
		SupportsSavedPlans:   true,
		SupportsRefresh:      true,
		SupportsPolicyNative: true,
		// The secrets_provider types Pulumi accepts for stack-state
		// encryption (engine.state.secrets_provider.type in config).
		SecretsProviderTypes: []string{"awskms", "gcpkms", "azurekeyvault", "hashivault", "passphrase"},
	}
}

// projectYAML is the minimum we parse from Pulumi.yaml.
type projectYAML struct {
	Name string `yaml:"name"`
}

// EnumerateStacks walks root looking for Pulumi.yaml files. For each,
// it records (project=<name>, path=<dir>) and enumerates stacks from
// sibling Pulumi.<stack>.yaml files.
func (e *Engine) EnumerateStacks(ctx context.Context, root string) ([]discovery.Stack, error) {
	repoRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer repoRoot.Close()

	var out []discovery.Stack
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip common noise dirs.
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "venv" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "Pulumi.yaml" && d.Name() != "Pulumi.yml" {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		// Read the project file through a root on the repo. WalkDir matches
		// entries with Lstat, so a Pulumi.yaml committed as a symlink is
		// matched by name and then followed by a plain read; os.Root refuses
		// to traverse out of the repo instead.
		relFile, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		project, err := readProjectName(repoRoot, relFile)
		if err != nil {
			return err
		}
		stackNames, err := stackNamesIn(dir)
		if err != nil {
			return err
		}
		for _, name := range stackNames {
			out = append(out, discovery.Stack{
				Project: project,
				Path:    rel,
				Name:    name,
				Env:     envGuess(name),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out, nil
}

// readProjectName reads a Pulumi.yaml through repoRoot; rel is its path
// relative to the repo root.
func readProjectName(repoRoot *os.Root, rel string) (string, error) {
	data, err := repoRoot.ReadFile(rel)
	if err != nil {
		return "", err
	}
	var p projectYAML
	if err := yaml.Unmarshal(data, &p); err != nil {
		return "", err
	}
	if p.Name == "" {
		// Fall back to the containing directory's name. rel is repo-relative,
		// so a Pulumi.yaml at the repo root has dir "." - resolve that to the
		// repo directory's own name, which is what the absolute path used to
		// produce.
		dir := filepath.Dir(rel)
		if dir == "." || dir == "" {
			return filepath.Base(repoRoot.Name()), nil
		}
		return filepath.Base(dir), nil
	}
	return p.Name, nil
}

func stackNamesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, "Pulumi.") {
			continue
		}
		if n == "Pulumi.yaml" || n == "Pulumi.yml" {
			continue
		}
		// "Pulumi.<name>.yaml" or ".yml"
		trimmed := strings.TrimSuffix(strings.TrimSuffix(n, ".yml"), ".yaml")
		trimmed = strings.TrimPrefix(trimmed, "Pulumi.")
		if trimmed == "" {
			continue
		}
		names = append(names, trimmed)
	}
	sort.Strings(names)
	return names, nil
}

func envGuess(stackName string) string {
	// Convention: if the stack name starts with an env prefix like
	// "prod/" or contains "-prod", use that. Otherwise the stack name is
	// itself the env. Good enough for Phase 1 rendering.
	if idx := strings.IndexAny(stackName, "/-"); idx > 0 {
		return stackName[:idx]
	}
	return stackName
}

// Preview runs `pulumi preview --json` for a single stack. The stack's
// path is used as cwd. Errors running the CLI are returned; non-zero exit
// with parseable JSON is treated as "preview failed" (populated Error on
// the result).
func (e *Engine) Preview(ctx context.Context, stack discovery.Stack, opts iac.PreviewOpts) (iac.PreviewResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}

	args := []string{"preview", "--stack", stack.Name, "--json", "--non-interactive"}
	if opts.Refresh {
		args = append(args, "--refresh=true")
	}
	if opts.SavePlanPath != "" {
		args = append(args, "--save-plan="+opts.SavePlanPath)
	}
	args = append(args, opts.ExtraArgs...)

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- e.Binary is engine.binary.path from operator config; args are built by this
	// adapter and passed as argv with no shell
	cmd := exec.CommandContext(runCtx, e.Binary, args...)
	iac.SetupGracefulStop(cmd, 0)
	cmd.Dir = cwd
	cmd.Env = commandEnv(opts.Env, opts.SavePlanPath != "")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := stdout.Bytes()

	// If stdout has JSON, parse regardless of exit code - Pulumi emits
	// the plan JSON even on non-fatal errors.
	if len(bytes.TrimSpace(out)) > 0 && out[0] == '{' {
		counts, short, diagErr, parseErr := parsePreview(out)
		if parseErr == nil {
			res := iac.PreviewResult{
				Counts:      counts,
				PlanSummary: short,
				FullPlan:    stderr.String() + string(out),
			}
			if diagErr != "" {
				res.Error = diagErr
			} else if runErr != nil {
				res.Error = runErr.Error()
			}
			// The human-readable diff needs a SECOND `pulumi preview --diff`
			// run: `--json` replaces the display output with the plan JSON,
			// so one invocation cannot produce both the structured counts and
			// the reviewer-facing diff (and reconstructing the diff from the
			// JSON steps would be a lossy reimplementation of Pulumi's
			// renderer). Skip that cost when the first run proved a clean
			// no-op - the renderer collapses no-op stacks to a table row and
			// never shows their diff.
			if res.Error != "" || counts.Total() > 0 {
				res.PlanDiff = e.previewDiff(ctx, stack, opts)
			}
			// Claim the saved plan only when one was asked for, the preview
			// is clean, and a non-empty file is actually there. Pulumi skips
			// writing the plan on a failed preview, and a caller that trusted
			// the requested path would then lock an apply to a file that does
			// not exist.
			if opts.SavePlanPath != "" && res.Error == "" {
				if fi, statErr := os.Stat(opts.SavePlanPath); statErr == nil && fi.Size() > 0 {
					res.PlanPath = opts.SavePlanPath
				}
			}
			return res, nil
		}
	}

	// No parseable stdout - bubble up stderr as error.
	msg := strings.TrimSpace(stderr.String())
	if msg == "" && runErr != nil {
		msg = runErr.Error()
	}
	if msg == "" {
		msg = "pulumi preview produced no output"
	}
	return iac.PreviewResult{
		Error:    msg,
		FullPlan: stderr.String() + string(out),
	}, nil
}

// previewDiff runs `pulumi preview --diff` and returns the human-readable
// colorizable diff output. Errors are non-fatal - caller uses empty string.
func (e *Engine) previewDiff(ctx context.Context, stack discovery.Stack, opts iac.PreviewOpts) string {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	args := []string{"preview", "--stack", stack.Name, "--diff", "--non-interactive"}

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- e.Binary is engine.binary.path from operator config; args are built by this
	// adapter and passed as argv with no shell
	cmd := exec.CommandContext(runCtx, e.Binary, args...)
	iac.SetupGracefulStop(cmd, 0)
	cmd.Dir = cwd
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(opts.Env)...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	out := strings.TrimSpace(stderr.String() + stdout.String())
	return formatDiff(out)
}

// formatDiff moves +/-/~ from after indentation to line start so GitHub's
// diff code fence colors them. Replaces ~ with ! for changed lines.
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

func flattenEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

// commandEnv builds the child environment. experimental is set only for the
// invocations that pass an update-plan flag: Pulumi keeps `--save-plan` and
// `--plan` behind PULUMI_EXPERIMENTAL, and turning that on globally would
// also unhide unrelated experimental behavior for every other command reeve
// runs. Scoping it to the two calls that need it keeps the blast radius to
// the feature the operator opted into.
func commandEnv(env map[string]string, experimental bool) []string {
	if len(env) == 0 && !experimental {
		return nil // inherit the parent environment unchanged
	}
	out := append(os.Environ(), flattenEnv(env)...)
	if experimental {
		out = append(out, "PULUMI_EXPERIMENTAL=true")
	}
	return out
}

// compile-time check: pulumi satisfies the full engine contract.
var _ iac.Engine = (*Engine)(nil)

// ErrNoPulumi is returned if the Pulumi binary is not on PATH.
var ErrNoPulumi = errors.New("pulumi binary not found on PATH")
