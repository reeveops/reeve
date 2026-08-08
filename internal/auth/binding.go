package auth

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Binding matches a stack ref and run mode to a set of provider names.
type Binding struct {
	StackPattern string // glob over "project/stack"
	Mode         Mode   // "" = all modes; otherwise preview|apply|drift
	Providers    []string
	Override     []string // replaces more-general providers of the same scope
	Local        []string // replaces same-scope providers in --local runs only
}

// ResolveOpts adjusts resolution for local runs.
type ResolveOpts struct {
	// Local activates each matched binding's Local list: entries replace
	// already-resolved providers of the same scope, after normal resolution.
	Local bool
	// LocalProviders is the --local-auth CLI override, applied after the
	// bindings' Local lists (CLI wins over config). Only consulted when
	// Local is true.
	LocalProviders []string
}

// ProviderDecl declares a named provider. The Type drives which adapter
// is constructed. Fields is the raw YAML fields the adapter consumes.
type ProviderDecl struct {
	Name   string
	Type   string
	Fields map[string]any
}

// Resolve returns the ordered, deduplicated list of provider names to
// activate for (stackRef, mode). Override lists replace earlier entries
// of the same scope; Providers lists union.
//
// Without provider declarations, scope falls back to a provider-name
// prefix approximation. Prefer ResolveWithDecls, which derives scope from
// the declared provider Type.
func Resolve(bindings []Binding, stackRef string, mode Mode) []string {
	return ResolveWithDecls(bindings, nil, stackRef, mode)
}

// ResolveWithDecls is Resolve with the provider declarations available so
// Override replaces earlier-matched providers of the same declared
// credential scope (e.g. every aws_* provider), not merely those sharing a
// name prefix. Names missing from decls fall back to the prefix
// approximation.
//
// Pure logic: actual credential acquisition lives in internal/auth/providers.
func ResolveWithDecls(bindings []Binding, decls map[string]ProviderDecl, stackRef string, mode Mode) []string {
	return ResolveWithOpts(bindings, decls, stackRef, mode, ResolveOpts{})
}

// ResolveWithOpts is ResolveWithDecls plus local-run substitution: when
// opts.Local is set, matched bindings' Local lists (then
// opts.LocalProviders) each replace already-resolved providers of the same
// scope. Applied as a second pass so a local substitute wins even over
// providers added by later, more-specific bindings.
func ResolveWithOpts(bindings []Binding, decls map[string]ProviderDecl, stackRef string, mode Mode, opts ResolveOpts) []string {
	// Sort bindings: general → specific. "More specific" = longer pattern
	// with fewer wildcards, plus mode-matched bindings override mode-agnostic.
	sorted := append([]Binding{}, bindings...)
	sortBindings(sorted)

	seen := map[string]bool{}
	var out []string
	for _, b := range sorted {
		if !matches(b, stackRef, mode) {
			continue
		}
		if len(b.Override) > 0 {
			// Override replaces everything from the same logical scope.
			for _, repl := range b.Override {
				out, seen = replaceScope(out, seen, repl, decls)
			}
		}
		for _, p := range b.Providers {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if !opts.Local {
		return out
	}
	for _, b := range sorted {
		if !matches(b, stackRef, mode) {
			continue
		}
		for _, repl := range b.Local {
			out, seen = replaceScope(out, seen, repl, decls)
		}
	}
	for _, repl := range opts.LocalProviders {
		out, seen = replaceScope(out, seen, repl, decls)
	}
	return out
}

// Validate checks bindings for conflicts. Two bindings matching the same
// stack with providers of identical logical scope and different names is
// an error (to avoid "which AWS role did I use?" ambiguity).
// Phase 4 approximation: same Type must not appear twice.
func Validate(bindings []Binding, declsByName map[string]ProviderDecl, stacks []string) error {
	for _, b := range bindings {
		for _, n := range b.Local {
			d, ok := declsByName[n]
			if !ok {
				return fmt.Errorf("binding local references undeclared provider %q", n)
			}
			if IsCIOnlyType(d.Type) {
				return fmt.Errorf("binding local provider %q has CI-only type %q - it acquires GitHub Actions OIDC tokens and can never succeed in a --local run; use aws_profile/aws_sso/gcloud_adc", n, d.Type)
			}
		}
	}
	for _, stack := range stacks {
		for _, mode := range []Mode{ModePreview, ModeApply, ModeDrift} {
			names := ResolveWithDecls(bindings, declsByName, stack, mode)
			typeSeen := map[string]string{}
			for _, n := range names {
				d, ok := declsByName[n]
				if !ok {
					return fmt.Errorf("binding references undeclared provider %q", n)
				}
				scope := scopeOfType(d.Type)
				if prev, exists := typeSeen[scope]; exists && prev != n {
					return fmt.Errorf("stack %s (%s): conflicting providers of scope %q: %s vs %s",
						stack, mode, scope, prev, n)
				}
				typeSeen[scope] = n
			}
		}
	}
	return nil
}

// IsCIOnlyType reports whether the provider type exchanges a GitHub
// Actions OIDC token for cloud credentials and therefore can never
// acquire outside CI. Used to lint dead `local:` entries and to hint on
// local acquire failures.
func IsCIOnlyType(t string) bool {
	switch t {
	case "aws_oidc", "gcp_wif", "azure_federated":
		return true
	}
	return false
}

// scopeOfType groups providers by the credential "domain" they live in.
// Two providers of the same scope bound to the same stack is an error.
func scopeOfType(t string) string {
	switch t {
	case "aws_oidc", "aws_profile", "aws_sso":
		return "aws"
	case "gcp_wif", "gcloud_adc":
		return "gcp"
	case "azure_federated":
		return "azure"
	case "github_app":
		return "github-identity"
	}
	// Secret managers, vault, env_passthrough: allow multiple.
	return "other:" + t
}

func matches(b Binding, ref string, mode Mode) bool {
	if b.Mode != "" && b.Mode != mode {
		return false
	}
	if b.StackPattern == "" {
		return true
	}
	ok, _ := doublestar.Match(b.StackPattern, ref)
	return ok
}

// sortBindings: general → specific, and mode-agnostic → mode-scoped.
// Iteration order matters: Override can only replace earlier-visited
// providers, so general must come first.
func sortBindings(bs []Binding) {
	for i := 1; i < len(bs); i++ {
		for j := i; j > 0 && moreSpecific(bs[j-1], bs[j]); j-- {
			bs[j], bs[j-1] = bs[j-1], bs[j]
		}
	}
}

// moreSpecific reports whether a is more specific than b - if so, swap
// pushes a rightward, landing after b.
func moreSpecific(a, b Binding) bool {
	if (a.Mode == "") != (b.Mode == "") {
		return a.Mode != ""
	}
	return specificity(a.StackPattern) > specificity(b.StackPattern)
}

func specificity(p string) int {
	score := 0
	for _, r := range p {
		switch r {
		case '*', '?':
			score -= 2
		default:
			score++
		}
	}
	return score
}

func replaceScope(list []string, seen map[string]bool, repl string, decls map[string]ProviderDecl) ([]string, map[string]bool) {
	out := list[:0]
	for _, x := range list {
		if sameScope(x, repl, decls) {
			delete(seen, x)
			continue
		}
		out = append(out, x)
	}
	if !seen[repl] {
		seen[repl] = true
		out = append(out, repl)
	}
	return out, seen
}

// sameScope reports whether providers a and b occupy the same credential
// scope. When both are declared, scope comes from the declared Type via
// scopeOfType - an override of an aws_oidc provider replaces every other
// AWS-scoped provider regardless of naming. When declarations are missing,
// fall back to comparing the first '-' segment of the names (e.g. aws-prod
// vs aws-prod-readonly), the historical v1 approximation.
func sameScope(a, b string, decls map[string]ProviderDecl) bool {
	da, okA := decls[a]
	db, okB := decls[b]
	if okA && okB {
		return scopeOfType(da.Type) == scopeOfType(db.Type)
	}
	aRoot := a
	if idx := strings.Index(a, "-"); idx > 0 {
		aRoot = a[:idx]
	}
	bRoot := b
	if idx := strings.Index(b, "-"); idx > 0 {
		bRoot = b[:idx]
	}
	return aRoot == bRoot
}
