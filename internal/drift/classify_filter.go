package drift

import (
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// Classification is drift-noise filtering applied to an engine diff before it
// is classified as drift. It is the runtime twin of drift.yaml's
// `classification:` block, decoupled from the config schema.
type Classification struct {
	// IgnoreProperties drops matching property changes. A resource whose only
	// changes are ignored properties stops counting as drift.
	IgnoreProperties []IgnoreProperty
	// IgnoreResources excludes whole resources by address/URN glob.
	IgnoreResources []string
	// TreatOrphanedAsDrift / TreatMissingAsDrift decide whether resources that
	// are tracked-but-gone (orphaned) or present-but-untracked (missing) count
	// as drift. Both default true (see cmd wiring): setting them false filters
	// that category out.
	TreatOrphanedAsDrift bool
	TreatMissingAsDrift  bool
}

// IgnoreProperty ignores a set of property paths on resources of a given
// type. ResourceType and Properties are globs (doublestar), matched against
// the full engine type token and the changed property paths respectively.
type IgnoreProperty struct {
	ResourceType string
	Properties   []string
}

// empty reports whether the classification would change nothing, so the
// runner can skip filtering entirely and keep the engine's raw verdict.
func (c *Classification) empty() bool {
	if c == nil {
		return true
	}
	return len(c.IgnoreProperties) == 0 &&
		len(c.IgnoreResources) == 0 &&
		c.TreatOrphanedAsDrift && c.TreatMissingAsDrift
}

// filter applies the classification to a structured resource set, returning
// the resources that still count as drift and whether anything was removed.
// It never mutates the input.
func (c *Classification) filter(resources []iac.ResourceChange) (kept []iac.ResourceChange, removed bool) {
	if c.empty() || len(resources) == 0 {
		return resources, false
	}
	kept = make([]iac.ResourceChange, 0, len(resources))
	for _, r := range resources {
		// 1. Whole-resource exclusion by address glob.
		if matchesAny(r.Address, c.IgnoreResources) {
			removed = true
			continue
		}
		// 2. treat_as_drift category filtering.
		if !c.TreatOrphanedAsDrift && r.Category == iac.DriftOrphaned {
			removed = true
			continue
		}
		if !c.TreatMissingAsDrift && r.Category == iac.DriftMissing {
			removed = true
			continue
		}
		// 3. Property-path filtering. Only update ops can be fully nullified
		//    by ignoring properties - a create/delete/replace is a
		//    resource-level change regardless of which properties differ.
		if r.Op == "update" && len(r.Paths) > 0 {
			remaining := c.retainedPaths(r)
			if len(remaining) == 0 {
				removed = true
				continue
			}
			if len(remaining) != len(r.Paths) {
				removed = true
				r.Paths = remaining
			}
		}
		kept = append(kept, r)
	}
	return kept, removed
}

// retainedPaths returns r's property paths that are NOT ignored for its type.
func (c *Classification) retainedPaths(r iac.ResourceChange) []string {
	ignored := c.ignoredPropsFor(r.Type)
	if len(ignored) == 0 {
		return r.Paths
	}
	out := make([]string, 0, len(r.Paths))
	for _, p := range r.Paths {
		if !matchesAny(p, ignored) {
			out = append(out, p)
		}
	}
	return out
}

// ignoredPropsFor collects the property globs configured for every
// ignore_properties entry whose resource_type glob matches typ.
func (c *Classification) ignoredPropsFor(typ string) []string {
	var out []string
	for _, ip := range c.IgnoreProperties {
		if ip.ResourceType == "" || wildcardMatch(ip.ResourceType, typ) {
			out = append(out, ip.Properties...)
		}
	}
	return out
}

// applyCounts recomputes the change counts and drifted-address set from a
// filtered resource list.
func applyCounts(resources []iac.ResourceChange) (summary.Counts, []string) {
	var c summary.Counts
	var addrs []string
	for _, r := range resources {
		switch r.Op {
		case "create":
			c.Add++
		case "update":
			c.Change++
		case "delete":
			c.Delete++
		case "replace":
			c.Replace++
		}
		if r.Address != "" {
			addrs = append(addrs, r.Address)
		}
	}
	return c, addrs
}

func matchesAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if wildcardMatch(p, s) {
			return true
		}
	}
	return false
}

// wildcardMatch matches a `*`/`?` glob where `*` spans any run of characters
// including separators. Unlike doublestar (used for stack refs), resource
// type tokens and URNs are not path-like: `/` and `:` are part of the
// identifier, so `aws:*` must match `aws:ec2/instance:Instance`. Linear-time
// with the classic star-backtrack algorithm; ASCII byte comparison suffices
// for engine type tokens and property paths.
func wildcardMatch(pattern, s string) bool {
	sx, px := 0, 0
	starPx, starSx := -1, 0
	for sx < len(s) {
		switch {
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == s[sx]):
			px++
			sx++
		case px < len(pattern) && pattern[px] == '*':
			starPx, starSx = px, sx
			px++
		case starPx != -1:
			starSx++
			px, sx = starPx+1, starSx
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}
