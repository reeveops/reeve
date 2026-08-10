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
		{"empty is transformed, not passed through", "", "project" + digestSep + shortHash("")},

		// Boundary and failure cases. A transformed name is the sanitised
		// form, digestSep, then the digest of the ORIGINAL.
		{"slash", "a/b", "a_b" + digestSep + shortHash("a/b")},
		{"underscore is not pass-through", "a_b", "a_b" + digestSep + shortHash("a_b")},
		{"traversal", "..", "project" + digestSep + shortHash("..")},
		{"sanitises to nothing", "///", "project" + digestSep + shortHash("///")},
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

// TestSlugComponentIsInjective proves the property by brute force over a
// wide input space rather than asserting it, because reasoning about it has
// been wrong twice: first a digest-only scheme let "a/b" and "a_b" alias,
// then a safe name shaped like a transformed one aliased it, then the empty
// string aliased whatever the fallback was named.
//
// Distinct names must never address the same lock object or plan artifact.
func TestSlugComponentIsInjective(t *testing.T) {
	t.Parallel()

	var inputs []string
	// Structured, path-like, degenerate and unicode names.
	inputs = append(inputs,
		"", "api", "prod", "stack", "project", "key", "my-project",
		"a/b", "a_b", "a.b", "a b", "a\\b", "a:b",
		"..", "../..", "///", "...", "   ", "_", "__", "___",
		"api/prod", "api_prod", "x/y/z", "x_y_z", "x/y_z",
		"st\u00e0ck", "\u00e9", "\u4f60\u597d",
	)
	// Names shaped exactly like this function's own output, which is how a
	// safe name can impersonate a transformed one.
	for _, seed := range []string{"a/b", "///", "", "api/prod", ".."} {
		base, _ := sanitizeComponent(seed)
		if strings.Trim(base, "_") == "" {
			base = "stack"
		}
		inputs = append(inputs, base+digestSep+shortHash(seed))
	}
	// Systematic single- and double-rune names over the boundary alphabet.
	alphabet := []string{"a", "Z", "0", "-", "_", "/", ".", " "}
	for _, x := range alphabet {
		inputs = append(inputs, x)
		for _, y := range alphabet {
			inputs = append(inputs, x+y, "p"+x+"q"+y)
		}
	}

	seen := make(map[string]string, len(inputs))
	for _, in := range inputs {
		got := SlugComponent(in, "stack")
		if got == "" {
			t.Errorf("SlugComponent(%q) produced an empty component", in)
		}
		if strings.ContainsAny(got, "/. ") {
			t.Errorf("SlugComponent(%q) = %q contains an unsafe rune", in, got)
		}
		if prev, dup := seen[got]; dup && prev != in {
			t.Errorf("COLLISION: %q and %q both slug to %q", prev, in, got)
		}
		seen[got] = in
	}
}

// TestSlugComponentEmptyDoesNotAliasFallback is the case reasoning missed:
// returning the fallback verbatim put it in the pass-through namespace, so
// an empty name and a stack literally named "stack" shared a lock object.
func TestSlugComponentEmptyDoesNotAliasFallback(t *testing.T) {
	t.Parallel()
	for _, fb := range []string{"stack", "project", "key"} {
		empty := SlugComponent("", fb)
		named := SlugComponent(fb, fb)
		if empty == named {
			t.Errorf("empty name and a component named %q both map to %q", fb, empty)
		}
		if !strings.Contains(empty, digestSep) {
			t.Errorf("empty input escaped the transformed namespace: %q", empty)
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
