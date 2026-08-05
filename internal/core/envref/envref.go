// Package envref resolves runtime "${env:NAME}" references. Config loading
// already expands these across every config field (internal/config), so this
// helper only covers values that reach a consumer without passing through
// that pass (channels built from hand-assembled configs, emitters constructed
// from raw options, tests). It is the single implementation - the slack,
// otel, and annotations packages previously carried identical copies.
package envref

import (
	"os"
	"strings"
)

// Expand unwraps an exact "${env:NAME}" reference to os.Getenv(NAME).
// Exact-match semantics only: the whole string must be a single reference;
// partial or unclosed references pass through unchanged.
func Expand(s string) string {
	if strings.HasPrefix(s, "${env:") && strings.HasSuffix(s, "}") {
		return os.Getenv(strings.TrimSuffix(strings.TrimPrefix(s, "${env:"), "}"))
	}
	return s
}
