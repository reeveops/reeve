// Package auth defines credential providers and the binding resolver.
// Zero-trust: short-lived federated credentials only - no long-lived
// secrets in CI.
// Providers live in internal/auth/providers/*. Binding resolution is pure
// logic and lives here.
package auth

// Mode identifies the run mode a binding applies to. An empty mode applies
// to all modes.
type Mode string

const (
	ModePreview Mode = "preview"
	ModeApply   Mode = "apply"
	ModeDrift   Mode = "drift"
)
