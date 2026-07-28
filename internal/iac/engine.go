// Package iac defines the engine-agnostic IaC interface. Each engine is its
// own package and registers itself: internal/iac/pulumi, internal/iac/terraform
// and internal/iac/tofu. The latter two are siblings, not one inside the
// other - they share an implementation in internal/iac/hcl but are separate
// engines with separate capabilities. Core never branches on engine name;
// consumers use Capabilities() for capability detection.
package iac

// Capabilities describes what an engine can do. Extended as new engines
// reveal needs.
type Capabilities struct {
	// SupportsSavedPlans reports that Preview can persist its plan
	// (PreviewOpts.SavePlanPath) and Apply can execute that exact artifact
	// (ApplyOpts.PlanPath) without recomputing it. This is what plan
	// locking is built on: false means every apply necessarily re-plans, so
	// "last apply wins" and the applied change set may differ from the one
	// that was reviewed.
	SupportsSavedPlans bool
	// SupportsRefresh reports that the engine implements Refresher, and
	// that PreviewOpts/ApplyOpts.Refresh are honored.
	SupportsRefresh      bool
	SupportsPolicyNative bool
	SecretsProviderTypes []string
}

// Engine is the full contract every IaC adapter satisfies: identity and
// capability detection plus the operational surface (enumerate, preview,
// apply, drift check). Callers resolve an Engine through New (the registry,
// keyed by config engine.type) and stay engine-agnostic; consumers that need
// less depend on the narrow per-operation interfaces (Enumerator, Previewer,
// Applier, DriftChecker, Refresher) instead.
type Engine interface {
	Name() string // display only - never branch on this
	Capabilities() Capabilities
	Enumerator
	Previewer
	Applier
	DriftChecker
	Refresher
}
