package blob

import (
	"strings"
	"testing"
)

func TestSlugComponent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Safe names pass through untouched - the common case, and what
		// keeps existing keys stable.
		{"plain", "api", "api"},
		{"hyphen kept", "my-project", "my-project"},
		{"underscore kept", "my_project", "my_project"},
		{"digits kept", "env2", "env2"},
		{"empty falls back", "", "project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SlugComponent(tc.in, "project"); got != tc.want {
				t.Fatalf("SlugComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSlugComponentNeutralisesUnsafeRunes(t *testing.T) {
	t.Parallel()
	// The names reaching key builders come from the repo under review, so
	// nothing outside the safe set may survive into a path component.
	for _, in := range []string{"../../etc/passwd", "a/b", "..", "my stack", "stàck", "///"} {
		got := SlugComponent(in, "project")
		if strings.ContainsAny(got, "/. ") {
			t.Errorf("SlugComponent(%q) = %q still contains an unsafe rune", in, got)
		}
	}
}

// TestSlugComponentDoesNotCollide is the point of the digest suffix.
// Replacement alone maps "a/b" and "a_b" onto the same component, which
// would point two distinct stacks at one lock object and let one stack's
// plan artifact overwrite another's.
func TestSlugComponentDoesNotCollide(t *testing.T) {
	t.Parallel()
	groups := [][]string{
		{"a/b", "a_b", "a.b", "a b"},
		{"///", "...", "   "},       // all degenerate, all distinct
		{"api/prod", "api_prod"},    // the realistic case
		{"x/y/z", "x_y_z", "x/y_z"}, //
	}
	for _, group := range groups {
		seen := map[string]string{}
		for _, in := range group {
			got := SlugComponent(in, "stack")
			if prev, dup := seen[got]; dup {
				t.Errorf("%q and %q both slug to %q", prev, in, got)
			}
			seen[got] = in
		}
	}
}

// TestSlugComponentIsIdempotent is load-bearing: lock keys are parsed back
// into components and re-derived, so slug(slug(x)) must equal slug(x).
func TestSlugComponentIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"api", "my-project", "my_project", "", "a/b", "../../etc/passwd",
		"///", "my stack", "stàck", "..",
	} {
		once := SlugComponent(in, "project")
		twice := SlugComponent(once, "project")
		if once != twice {
			t.Errorf("SlugComponent(%q) = %q, but re-slugging gives %q", in, once, twice)
		}
	}
}

// TestSlugComponentNormalisesFallback stops an unsafe fallback from
// bypassing the invariant the rest of the function exists to hold.
func TestSlugComponentNormalisesFallback(t *testing.T) {
	t.Parallel()
	got := SlugComponent("", "../outside")
	if strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Fatalf("unsafe fallback survived: %q", got)
	}
	// A fallback with nothing usable is replaced outright rather than
	// producing an empty path segment.
	if got := SlugComponent("", "///"); got == "" || strings.Contains(got, "/") {
		t.Fatalf("degenerate fallback produced %q", got)
	}
}
