// Package terraform registers the Terraform engine (engine.type: terraform).
//
// The implementation lives in internal/iac/hcl, shared with OpenTofu. This
// package contributes only what is specific to Terraform, so a Terraform-only
// divergence has an obvious home. See openspec/specs/iac/spec.md for what
// would force the shared package to split for real.
package terraform

import (
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/iac/hcl"
)

// Dialect is Terraform's declaration of how it differs from its siblings.
var Dialect = hcl.Dialect{
	TypeName: "terraform",
	Display:  "Terraform",
	Binary:   "terraform",
	// Terraform reads only .tf. It does not read .tofu, so enumerating one
	// would hand back a stack terraform then refuses to plan.
	SourceExts: []string{".tf"},
	Caps: iac.Capabilities{
		// Apply consumes the exact saved plan file produced by its own plan
		// step (plan-what-you-apply parity).
		SupportsSavedPlans: true,
		// Drift checks run `plan -refresh-only`, which evaluates live
		// infrastructure without mutating state.
		SupportsRefresh:      true,
		SupportsPolicyNative: false,
		// Terraform has no language-level state encryption: it is a backend
		// concern (S3 SSE and friends), so there is no engine-side secrets
		// provider to configure. This is where Terraform and OpenTofu
		// genuinely differ - see the tofu package.
		SecretsProviderTypes: nil,
	},
}

// init self-registers the engine; blank-importing this package
// (internal/iac/all does for the default set) is what compiles it in.
func init() { iac.Register(Dialect.TypeName, hcl.Constructor(Dialect)) }
