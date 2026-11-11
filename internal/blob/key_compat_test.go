package blob

import (
	"strings"
	"testing"
)

// legacySlug replicates the pre-fix key formula, so the ordinary-name
// assertion below is against the EXACT mapping that produced every existing
// lock and audit object - not against a re-derivation of it.
func legacySlug(s, fallback string) string {
	if s == "" {
		return legacySlug(fallback, "key")
	}
	if isPassthrough(s) {
		return s
	}
	sanitised, _ := sanitizeComponent(s)
	if strings.Trim(sanitised, "_") == "" {
		return "key" + digestSep + shortHash(s)
	}
	return sanitised + digestSep + shortHash(s)
}

// TestSlugOrdinaryNamesKeepKeys is the gate for the empty-component fix:
// every ordinary project/stack name keeps its pre-fix key, so no existing
// lock or audit object is orphaned. Only degenerate inputs change: an empty
// component no longer aliases the literal fallback name
// (SlugComponent("", "stack") != SlugComponent("stack", ...)).
func TestSlugOrdinaryNamesKeepKeys(t *testing.T) {
	names := []string{"api", "my-stack-1", "A", "a_b", "n1", "prod_eu"}
	for _, n := range names {
		for _, fb := range []string{"project", "stack"} {
			if got, want := SlugComponent(n, fb), legacySlug(n, fb); got != want {
				t.Errorf("ORDINARY name %q changed key: %q != %q", n, got, want)
			}
		}
	}
	empty, literal := SlugComponent("", "stack"), SlugComponent("stack", "stack")
	if empty == literal {
		t.Errorf("empty stack must not alias a stack literally named \"stack\": both %q", empty)
	}
	if !strings.Contains(empty, digestSep) {
		t.Errorf("the empty component must land in the transformed namespace: %q", SlugComponent("", "stack"))
	}
}
