package blob

import "testing"

func TestSlugComponent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "api", "api"},
		{"hyphen kept", "my-project", "my-project"},
		{"digits kept", "env2", "env2"},
		// The names reaching key builders come from the repo under review.
		{"path separator neutralised", "../../etc/passwd", "______etc_passwd"},
		{"slash neutralised", "a/b", "a_b"},
		{"dots neutralised", "..", "project"},
		{"spaces neutralised", "my stack", "my_stack"},
		{"unicode neutralised", "stàck", "st_ck"},
		{"empty falls back", "", "project"},
		{"all-invalid falls back", "///", "project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugComponent(tc.in, "project")
			if got != tc.want {
				t.Fatalf("SlugComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Idempotence is load-bearing: lock keys are parsed back into
			// components and re-derived, so slug(slug(x)) must equal slug(x).
			if again := SlugComponent(got, "project"); again != got {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}
