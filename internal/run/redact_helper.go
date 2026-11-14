package run

import (
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/redact"
	"github.com/reeveops/reeve/internal/log"
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

// InstallInvocationRedactor builds the command's invocation redactor (pattern
// rules + shared config) and installs it as the process log redactor. Every
// command that resolves credentials or registers secret values calls this at
// entry, so a registered value can never reach a log line; a run without it
// would log on the pattern-rule floor only.
func InstallInvocationRedactor(shared *schemas.Shared) *redact.Redactor {
	r := BuildRedactor(shared)
	log.SetRedactor(r)
	return r
}

// RegisterCredentialValues registers resolved credential values with the
// invocation redactor. The redactor is the installed instance, so the log
// handler masks them from the moment of registration.
func RegisterCredentialValues(r *redact.Redactor, env map[string]string) {
	for _, v := range env {
		if v != "" {
			r.AddSecret(v)
		}
	}
}
