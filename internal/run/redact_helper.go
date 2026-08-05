package run

import (
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/policy"
)

// BuildRedactor constructs a central Redactor from shared.yaml (Phase 6+).
// For now we scaffold the hook; later phases extend shared.yaml with an
// explicit redaction rule list.
func BuildRedactor(s *schemas.Shared) *redact.Redactor {
	r := redact.New()
	// Defaults: strip common cloud credential patterns if they leak.
	r.AddRule(`AKIA[0-9A-Z]{16}`)                            // AWS access key id
	r.AddRule(`ASIA[0-9A-Z]{16}`)                            // AWS STS access key id
	r.AddRule(`aws_secret_access_key\s*=\s*[A-Za-z0-9/+=]+`) // crude aws secret kv
	r.AddRule(`ghp_[A-Za-z0-9]{36,}`)                        // GitHub PAT
	r.AddRule(`gho_[A-Za-z0-9]{36,}`)                        // GitHub OAuth
	r.AddRule(`ghs_[A-Za-z0-9]{36,}`)                        // GitHub server-to-server
	r.AddRule(`xox[baprs]-[A-Za-z0-9-]+`)                    // Slack tokens
	_ = s
	return r
}

// policyRender wraps policy.RenderSection so run/apply.go can compose it
// into FullPlan without importing internal/policy directly.
func policyRender(results []policy.Result) string {
	return policy.RenderSection(results)
}
