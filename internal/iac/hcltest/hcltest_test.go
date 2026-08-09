package hcltest

import (
	"path/filepath"
	"testing"

	"github.com/reeveops/reeve/internal/iac/hcl"
)

func TestFirstLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single line", "tofu v1.9.0", "tofu v1.9.0"},
		{"takes the first of many", "line one\nline two\n", "line one"},
		// Splitting CRLF output on \n leaves the \r behind, which shows up
		// as a stray carriage return in the middle of the skip message.
		{"crlf", "line one\r\nline two\r\n", "line one"},
		{"leading and trailing space", "  padded  \n", "padded"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstLine([]byte(tc.in)); got != tc.want {
				t.Fatalf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveBinaryProbesTheOverride pins that an override binary is
// validated the same way a PATH lookup is. Handing back an unrunnable
// override unprobed only moved the failure later, into the engine calls,
// where it read as an adapter bug rather than a bad REEVE_*_BIN.
//
// The inner test skips when ResolveBinary behaves (that is the whole point)
// and fails only if ResolveBinary returns a binary it never proved runnable.
func TestResolveBinaryProbesTheOverride(t *testing.T) {
	const overrideEnv = "REEVE_TEST_FAKE_ENGINE_BIN"
	ok := t.Run("unrunnable override", func(t *testing.T) {
		t.Setenv(overrideEnv, filepath.Join(t.TempDir(), "does-not-exist"))
		bin := ResolveBinary(t, hcl.Dialect{Binary: "definitely-not-a-real-engine"}, overrideEnv)
		t.Errorf("returned %q without probing that it runs", bin)
	})
	if !ok {
		t.Fatal("an unrunnable override was accepted; it must skip (or fail under REEVE_ENGINE_TESTS_REQUIRED)")
	}
}
