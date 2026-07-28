package discovery

import (
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Stack is the normalized shape produced by the engine adapter. Core owns
// filtering and change-mapping over a flat []Stack.
type Stack struct {
	Project string // e.g. "api"
	Path    string // repo-relative, e.g. "projects/api"
	Name    string // stack name, e.g. "prod"
	Env     string // derived from Name or explicitly declared; used in rendering
}

// Ref returns "project/name" - the canonical identifier used in rules,
// comments, and bucket keys.
func (s Stack) Ref() string { return s.Project + "/" + s.Name }

// Declaration is one entry from engine config: either a literal
// (Project + Path) or a Pattern (glob over paths).
type Declaration struct {
	Project string
	Path    string
	Pattern string
	Stacks  []string // which stack names apply
}

// Filter is the exclude set from engine config.
type Filter struct {
	// PathPatterns exclude by stack path glob (doublestar).
	PathPatterns []string
	// StackPatterns exclude by "project/stack" glob.
	StackPatterns []string
}

// ChangeMapping drives which stacks are "affected" by a changed-files set.
type ChangeMapping struct {
	IgnoreChanges []string            // globs of paths to ignore entirely
	ExtraTriggers map[string][]string // project -> extra path patterns that trigger it
	// Scope: "auto" (default) previews all stacks when a changed file maps to
	// no specific stack; "pulumi_only" never broadens.
	Scope string
}

// Change-mapping scope values (mirrored in config schema).
const (
	ScopeAuto       = "auto"
	ScopePulumiOnly = "pulumi_only"
)

// DefaultSkipGlobs are non-load-bearing file types that never affect a Pulumi
// run: docs and image assets. A change touching ONLY these is reported as
// docs-only and runs nothing. Merged with user IgnoreChanges. Deliberately
// excludes docs/ directories - those can hold config or program-read data.
var DefaultSkipGlobs = []string{
	"**/*.md", "*.md",
	"**/*.markdown", "*.markdown",
	"**/*.adoc", "*.adoc",
	"**/*.asciidoc", "*.asciidoc",
	"**/*.rst", "*.rst",
	"**/*.txt", "*.txt",
	"**/LICENSE", "LICENSE",
	"**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.svg", "**/*.webp",
}

// AffectReason explains why Affected returned the set it did, so callers can
// surface context in the PR comment.
type AffectReason int

const (
	// ReasonMatched: changed files mapped to specific stacks (normal path).
	ReasonMatched AffectReason = iota
	// ReasonDocsOnly: every changed file was skippable (docs/images); no run.
	ReasonDocsOnly
	// ReasonBroadened: a changed file mapped to no stack and scope=auto, so
	// every declared stack is previewed.
	ReasonBroadened
)

// AffectedResult bundles the affected stacks with the reason.
type AffectedResult struct {
	Stacks []Stack
	Reason AffectReason
	// Unmapped lists changed files (post-skip) that matched no stack; only
	// populated when Reason == ReasonBroadened, for the explanation header.
	Unmapped []string
	// Matched is the set of stacks whose paths (or extra triggers) actually
	// matched a changed file, regardless of Reason. When Reason is
	// ReasonBroadened, Stacks is every stack but Matched is still just these
	// - so a caller can distinguish "might be affected" from "demonstrably
	// affected". Apply uses this; preview uses Stacks.
	Matched []Stack
}

// Resolve expands declarations into concrete stacks and applies filters.
// enumerated is the flat []Stack the engine adapter produced. decls tells
// us which (project, stack-name) pairs we care about.
func Resolve(enumerated []Stack, decls []Declaration, filter Filter) []Stack {
	// Build declaration index: path -> stackNames, project -> stackNames.
	declaredByPath := map[string][]string{}
	declaredByPattern := []Declaration{}
	for _, d := range decls {
		if d.Pattern != "" {
			declaredByPattern = append(declaredByPattern, d)
			continue
		}
		if d.Path != "" {
			declaredByPath[d.Path] = append(declaredByPath[d.Path], d.Stacks...)
		}
	}

	keep := make([]Stack, 0, len(enumerated))
	for _, s := range enumerated {
		if !declared(s, declaredByPath, declaredByPattern) {
			continue
		}
		if excluded(s, filter) {
			continue
		}
		keep = append(keep, s)
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].Ref() < keep[j].Ref() })
	return keep
}

func declared(s Stack, byPath map[string][]string, patterns []Declaration) bool {
	if names, ok := byPath[s.Path]; ok && containsStack(names, s.Name) {
		return true
	}
	for _, d := range patterns {
		ok, _ := doublestar.Match(d.Pattern, s.Path)
		if ok && containsStack(d.Stacks, s.Name) {
			return true
		}
	}
	return false
}

func excluded(s Stack, f Filter) bool {
	for _, p := range f.PathPatterns {
		if ok, _ := doublestar.Match(p, s.Path); ok {
			return true
		}
	}
	for _, p := range f.StackPatterns {
		if ok, _ := doublestar.Match(p, s.Ref()); ok {
			return true
		}
	}
	return false
}

func containsStack(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// Affected returns the subset of stacks whose paths (or ExtraTriggers)
// intersect the given changed files. Thin wrapper over AffectedDetailed for
// callers that only need the stack list.
func Affected(stacks []Stack, changedFiles []string, cm ChangeMapping) []Stack {
	return AffectedDetailed(stacks, changedFiles, cm).Stacks
}

// AffectedDetailed maps changed files to stacks and explains the result.
//
// Order:
//  1. Drop skippable files (DefaultSkipGlobs + IgnoreChanges).
//  2. If nothing remains -> ReasonDocsOnly, no stacks.
//  3. Match remaining files to specific stacks (path / per-stack config /
//     ExtraTriggers).
//  4. Files that matched no stack are "unmapped". With Scope=auto (default)
//     and at least one unmapped file -> ReasonBroadened, preview every stack.
//     With Scope=pulumi_only, unmapped files are ignored.
func AffectedDetailed(stacks []Stack, changedFiles []string, cm ChangeMapping) AffectedResult {
	skip := append(append([]string{}, DefaultSkipGlobs...), cm.IgnoreChanges...)
	filtered := make([]string, 0, len(changedFiles))
	for _, f := range changedFiles {
		if matchesAny(skip, []string{f}) {
			continue
		}
		filtered = append(filtered, f)
	}

	if len(filtered) == 0 {
		return AffectedResult{Reason: ReasonDocsOnly}
	}

	out := make([]Stack, 0, len(stacks))
	matchedFiles := map[string]bool{}
	for _, s := range stacks {
		hit := false
		if intersectsPath(s, filtered) {
			hit = true
		} else if triggers, ok := cm.ExtraTriggers[s.Project]; ok && matchesAny(triggers, filtered) {
			hit = true
		}
		if hit {
			out = append(out, s)
			markMatched(matchedFiles, s, filtered, cm)
		}
	}

	// Unmapped: post-skip files that no stack claimed.
	var unmapped []string
	for _, f := range filtered {
		if !matchedFiles[f] {
			unmapped = append(unmapped, f)
		}
	}

	if len(unmapped) > 0 && cm.Scope != ScopePulumiOnly {
		// Broadening REPLACES the precise matches with every stack, which is
		// right for "preview what might be affected" and wrong for deciding
		// what to apply. Matched carries the precise set through so a caller
		// that must not widen its blast radius (apply) can use it.
		return AffectedResult{Stacks: stacks, Matched: out, Reason: ReasonBroadened, Unmapped: unmapped}
	}
	return AffectedResult{Stacks: out, Matched: out, Reason: ReasonMatched}
}

// markMatched records which filtered files caused stack s to be selected, so
// the caller can compute the unmapped remainder.
func markMatched(matched map[string]bool, s Stack, filtered []string, cm ChangeMapping) {
	for _, f := range filtered {
		if fileMatchesStack(s, f) {
			matched[f] = true
			continue
		}
		if triggers, ok := cm.ExtraTriggers[s.Project]; ok && matchesAny(triggers, []string{f}) {
			matched[f] = true
		}
	}
}

// intersectsPath reports whether any changed file affects the given stack.
//
// A stack's path is a directory, but a single directory can hold many stacks
// (one shared Pulumi.yaml plus a Pulumi.<name>.yaml per stack). So a change to
// a sibling's per-stack config (Pulumi.<other>.yaml) must NOT pull this stack
// in. Rules:
//   - "Pulumi.<this stack name>.yaml" in the dir -> affects only this stack.
//   - "Pulumi.<other name>.yaml" in the dir       -> belongs to a sibling; ignored.
//   - any other file in/under the dir (program code, the shared Pulumi.yaml,
//     nested files) -> shared, affects every stack in the dir.
func intersectsPath(s Stack, changed []string) bool {
	for _, f := range changed {
		if fileMatchesStack(s, f) {
			return true
		}
	}
	return false
}

// fileMatchesStack reports whether a single changed file affects stack s by
// path. Per-stack config files (Pulumi.<name>.yaml) count only for their own
// stack; shared dir files (program code, Pulumi.yaml, nested) count for all
// stacks in the dir.
func fileMatchesStack(s Stack, f string) bool {
	stackPath := s.Path
	root := stackPath == "." || stackPath == ""
	prefix := stackPath + "/"
	if root {
		prefix = ""
	}
	if !root && f != stackPath && !strings.HasPrefix(f, prefix) {
		return false
	}
	rel := f
	if !root {
		rel = strings.TrimPrefix(f, prefix)
	}
	if name, ok := stackConfigName(rel); ok {
		return name == s.Name
	}
	return true
}

// stackConfigName returns the stack name encoded in a "Pulumi.<name>.yaml"
// (or .yml) file living directly in a stack directory. ok is false for the
// shared "Pulumi.yaml"/"Pulumi.yml" or for any path with a slash (a file in a
// subdirectory, which is shared program code rather than a stack config).
func stackConfigName(rel string) (string, bool) {
	if strings.Contains(rel, "/") {
		return "", false
	}
	if rel == "Pulumi.yaml" || rel == "Pulumi.yml" {
		return "", false
	}
	if !strings.HasPrefix(rel, "Pulumi.") {
		return "", false
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(rel, ".yml"), ".yaml")
	trimmed = strings.TrimPrefix(trimmed, "Pulumi.")
	if trimmed == "" || trimmed == rel {
		return "", false
	}
	return trimmed, true
}

func matchesAny(patterns, files []string) bool {
	for _, pat := range patterns {
		for _, f := range files {
			if ok, _ := doublestar.PathMatch(pat, f); ok {
				return true
			}
		}
	}
	return false
}
