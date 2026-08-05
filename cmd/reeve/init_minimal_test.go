//go:build reeve_minimal

package main

import (
	"strings"
	"testing"
)

// In a minimal build, an interactive session (TTY, no -n) must fail fast
// with a pointer at --non-interactive instead of trying to launch the
// excluded wizard.
func TestInitMinimalBuildRejectsInteractive(t *testing.T) {
	fakeTTY(t, true)
	pulumiRepo(t)

	out, err := runReeve(t, "init")
	if err == nil {
		t.Fatalf("expected error, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not available in this build") ||
		!strings.Contains(err.Error(), "--non-interactive") {
		t.Errorf("unexpected error: %v", err)
	}
}
