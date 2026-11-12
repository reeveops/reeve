package run

import (
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/redact"
	"github.com/reeveops/reeve/internal/policy"
)

// BuildRedactor constructs a central Redactor from shared.yaml (Phase 6+).
// For now we scaffold the hook; later phases extend shared.yaml with an
// explicit redaction rule list.
func BuildRedactor(s *schemas.Shared) *redact.Redactor {
	// The credential-rule floor lives in core/redact so the log handler can
	// use the same list before any run layer exists.
	r := redact.NewDefault()
	_ = s
	return r
}

// policyRender wraps policy.RenderSection so run/apply.go can compose it
// into FullPlan without importing internal/policy directly.
func policyRender(results []policy.Result) string {
	return policy.RenderSection(results)
}
