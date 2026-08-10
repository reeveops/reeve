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
		{"digits kept", "env2", "env2"},
		{"empty falls back", "", "project"},

		// Boundary and failure cases. A transformed name is the sanitised
		// form, digestSep, then the digest of the ORIGINAL.
		{"slash", "a/b", "a_b" + digestSep + shortHash("a/b")},
		{"underscore is not pass-through", "a_b", "a_b" + digestSep + shortHash("a_b")},
		{"traversal", "..", "key" + digestSep + shortHash("..")},
		{"sanitises to nothing", "///", "key" + digestSep + shortHash("///")},
		{"space", "my stack", "my_stack" + digestSep + shortHash("my stack")},
		{"unicode", "st\u00e0ck", "st_ck" + digestSep + shortHash("st\u00e0ck")},
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

// TestSlugComponentIsInjective is the property that actually matters:
// distinct names must never address the same lock object or plan artifact.
//
// The transformed namespace always contains digestSep, and a pass-through
// name can never contain it, so the two classes cannot alias. That
// disjointness is what closes the case a digest alone leaves open: a
// component literally named "a_b<digestSep><digest of a/b>" would otherwise
// be safe, pass through unchanged, and collide with SlugComponent("a/b").
func TestSlugComponentIsInjective(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"api", "my-project", "a/b", "a_b", "a.b", "a b",
		"///", "...", "   ", "..", "api/prod", "api_prod",
		"x/y/z", "x_y_z", "x/y_z", "st\u00e0ck",
		// The adversarial one: a SAFE name shaped exactly like the slug of
		// "a/b". A PR could add a stack with this name to share another
		// stack's lock.
		"a_b" + digestSep + shortHash("a/b"),
		"key" + digestSep + shortHash("///"),
	}
	seen := map[string]string{}
	for _, in := range inputs {
		got := SlugComponent(in, "stack")
		if prev, dup := seen[got]; dup {
			t.Errorf("%q and %q both slug to %q", prev, in, got)
		}
		seen[got] = in
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
