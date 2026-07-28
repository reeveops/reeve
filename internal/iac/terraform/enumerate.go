package terraform

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/FynxLabs/reeve/internal/core/discovery"
)

const defaultWorkspace = "default"

// rootModuleMarker matches .tf content that marks a directory as a root
// module: a terraform{} block (backend/required_providers live inside it)
// or a provider configuration block. Child modules referenced as module
// sources typically live under modules/ (excluded from the walk) - a
// module dir outside modules/ that carries its own terraform{}/provider
// block is indistinguishable from a root module and will enumerate; use
// engine.filters.exclude for those layouts.
var rootModuleMarker = regexp.MustCompile(`(?m)^\s*(terraform\s*\{|provider\s+")`)

// EnumerateStacks walks root for terraform root modules. Each root-module
// directory is a project; its stacks are the workspaces:
//
//   - Declared in engine config (stacks: entries matching the dir): the
//     declaration is authoritative - no `workspace list` call, no init
//     needed.
//   - Undeclared: best-effort `terraform workspace list` (needs a prior
//     init for remote backends). If listing fails, the dir enumerates as
//     <project>/default with a log line - never an opaque failure.
func (e *Engine) EnumerateStacks(ctx context.Context, root string) ([]discovery.Stack, error) {
	dirs, err := rootModuleDirs(root, e.variant.sourceExts())
	if err != nil {
		return nil, err
	}
	projects := projectNames(root, dirs)

	var out []discovery.Stack
	for _, dir := range dirs {
		names := e.declaredStackNames(dir)
		if len(names) == 0 {
			names = e.workspaceNames(ctx, filepath.Join(root, dir), dir)
		}
		for _, n := range names {
			out = append(out, discovery.Stack{
				Project: projects[dir],
				Path:    dir,
				Name:    n,
				Env:     envGuess(n),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out, nil
}

// ScanStacks enumerates root modules purely from the filesystem: every
// root-module dir reports its default workspace, no CLI calls. Used by
// `reeve init`'s repo scan, where no engine config exists yet and the
// binary may be absent (the same scan-without-binary guarantee the pulumi
// adapter's file-based enumeration gives init).
func ScanStacks(root string) ([]discovery.Stack, error) {
	// Init-time discovery, before an engine.type is chosen: accept both
	// extensions so a .tofu-only repo is found at all. Which engine the user
	// ends up declaring is a separate decision.
	dirs, err := rootModuleDirs(root, allSourceExts)
	if err != nil {
		return nil, err
	}
	projects := projectNames(root, dirs)
	out := make([]discovery.Stack, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, discovery.Stack{
			Project: projects[dir],
			Path:    dir,
			Name:    defaultWorkspace,
			Env:     defaultWorkspace,
		})
	}
	return out, nil
}

// Root-module source extensions. Terraform reads only .tf. OpenTofu reads
// both .tf and .tofu, and where the same base name exists as both, the .tofu
// file wins - so a repo written for OpenTofu may contain no .tf at all.
// Matching only .tf made such a repo enumerate zero stacks, silently.
const (
	extTF   = ".tf"
	extTofu = ".tofu"
)

// allSourceExts is every extension any supported variant reads.
var allSourceExts = []string{extTF, extTofu}

// sourceExts is the set of extensions this variant's binary actually reads.
// Accepting .tofu under engine.type: terraform would enumerate a stack the
// terraform CLI then refuses to plan.
func (v Variant) sourceExts() []string {
	if v.TypeName == OpenTofu.TypeName {
		return allSourceExts
	}
	return []string{extTF}
}

func hasSourceExt(name string, exts []string) bool {
	for _, e := range exts {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

// rootModuleDirs returns repo-relative directories that look like terraform
// root modules. Directories named "modules" (shared child modules) and the
// usual noise dirs are skipped entirely. The walk and reads go through a
// root-scoped filesystem (os.Root) so a symlink swapped in mid-walk cannot
// redirect a read outside the scan root (gosec G122 TOCTOU).
func rootModuleDirs(root string, exts []string) ([]string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	rfs := r.FS()

	var dirs []string
	err = fs.WalkDir(rfs, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "venv", ".venv", ".terraform", "modules":
				if p != "." {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !hasSourceExt(d.Name(), exts) {
			return nil
		}
		rel := path.Dir(p) // fs paths are already slash-separated and root-relative
		for _, seen := range dirs {
			if seen == rel {
				return nil
			}
		}
		data, err := fs.ReadFile(rfs, p)
		if err != nil {
			return err
		}
		if rootModuleMarker.Match(data) {
			dirs = append(dirs, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

// projectNames maps each root-module dir to its project name: the directory
// base name, or a slash-flattened relative path when base names collide
// (keeps stack refs "project/stack" unambiguous).
func projectNames(root string, dirs []string) map[string]string {
	base := func(dir string) string {
		if dir == "." || dir == "" {
			abs, err := filepath.Abs(root)
			if err != nil {
				return "root"
			}
			return filepath.Base(abs)
		}
		return filepath.Base(dir)
	}
	counts := map[string]int{}
	for _, d := range dirs {
		counts[base(d)]++
	}
	out := make(map[string]string, len(dirs))
	for _, d := range dirs {
		name := base(d)
		if counts[name] > 1 && d != "." && d != "" {
			name = strings.ReplaceAll(d, "/", "-")
		}
		out[d] = name
	}
	return out
}

// declaredStackNames returns the config-declared stack names for a
// root-module dir (literal path match or doublestar pattern), deduped and
// sorted. Empty when nothing is declared for the dir.
func (e *Engine) declaredStackNames(dir string) []string {
	seen := map[string]bool{}
	for _, d := range e.decls {
		match := (d.Path != "" && d.Path == dir)
		if !match && d.Pattern != "" {
			match = matchPattern(d.Pattern, dir)
		}
		if !match {
			continue
		}
		for _, n := range d.Stacks {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func matchPattern(pattern, dir string) bool {
	ok, err := doublestar.Match(pattern, dir)
	return err == nil && ok
}

// workspaceNames lists workspaces via the CLI, falling back to ["default"]
// with a log line when listing isn't possible (binary missing, backend not
// initialized). The fallback is deliberate: enumeration must not hard-fail
// just because init hasn't run yet.
func (e *Engine) workspaceNames(ctx context.Context, cwd, rel string) []string {
	res, err := e.run(ctx, cwd, nil, e.Binary, "workspace", "list")
	if err != nil || res.ExitCode != 0 {
		slog.Info("terraform workspace list unavailable; assuming the default workspace (declare stacks in engine config or run init to enumerate workspaces)",
			"engine", e.variant.TypeName, "dir", rel, "reason", firstLine(failureMessage(string(res.Stderr), err)))
		return []string{defaultWorkspace}
	}
	var names []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if line != "" {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		return []string{defaultWorkspace}
	}
	sort.Strings(names)
	return names
}

// envGuess mirrors the pulumi adapter's convention: a stack name's leading
// segment (before "/" or "-") is the env, else the name itself.
func envGuess(stackName string) string {
	if idx := strings.IndexAny(stackName, "/-"); idx > 0 {
		return stackName[:idx]
	}
	return stackName
}
