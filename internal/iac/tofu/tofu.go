// Package tofu registers the OpenTofu engine (engine.type: tofu).
//
// The implementation lives in internal/iac/hcl, shared with Terraform. This
// package contributes only what is specific to OpenTofu. It exists as its own
// package because OpenTofu is no longer a drop-in fork - it reads a different
// source extension and encrypts state in the language - and each new
// divergence should land here rather than as a conditional in shared code.
// See openspec/specs/iac/spec.md for what would force a real split.
package tofu

import (
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/iac/hcl"
)

// Dialect is OpenTofu's declaration of how it differs from Terraform.
var Dialect = hcl.Dialect{
	TypeName: "tofu",
	Display:  "OpenTofu",
	Binary:   "tofu",
	// OpenTofu reads .tofu in addition to .tf, and where a base name exists
	// as both, the .tofu file wins. A repo written for OpenTofu can contain
	// no .tf at all.
	SourceExts: []string{".tf", ".tofu"},
	Caps: iac.Capabilities{
		SupportsSavedPlans:   true,
		SupportsRefresh:      true,
		SupportsPolicyNative: false,
		// Unlike Terraform, OpenTofu encrypts state in the language: an
		// `encryption` block selects a key provider. Reporting nil here
		// (which the shared Terraform/OpenTofu adapter used to do) told
		// callers OpenTofu had no engine-side secrets provider, which is
		// simply wrong.
		SecretsProviderTypes: []string{"pbkdf2", "awskms", "gcpkms", "openbao"},
	},
}

// init self-registers the engine; blank-importing this package
// (internal/iac/all does for the default set) is what compiles it in.
func init() { iac.Register(Dialect.TypeName, hcl.Constructor(Dialect)) }
