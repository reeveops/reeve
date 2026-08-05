package iac

import (
	"context"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
)

// RefreshOpts configures a refresh call.
type RefreshOpts struct {
	Cwd        string
	ExtraArgs  []string
	Env        map[string]string
	TimeoutSec int
	// PreviewOnly asks for a read-only answer: what a refresh WOULD
	// reconcile, with no write to engine state.
	//
	// Contract: an engine that cannot answer read-only MUST return an error
	// rather than run a writing refresh. Silently upgrading a dry run to a
	// state mutation is the one failure mode this flag exists to prevent.
	PreviewOnly bool
}

// RefreshResult is what the engine returns per stack after a refresh.
//
// Counts describe STATE reconciliation, not infrastructure changes: a
// refresh never creates, updates or destroys a resource in the provider. A
// "delete" here means "the resource is gone from the cloud and was dropped
// from state", not "reeve deleted it".
type RefreshResult struct {
	Counts summary.Counts
	// Summary is the short human line (same shape as PreviewResult.PlanSummary).
	Summary string
	// Output is the raw CLI output, redacted upstream.
	Output string
	// Error is non-empty when the refresh failed for this stack. As
	// everywhere else in this package, an adapter reports an engine-level
	// failure here with a nil error return; a non-nil error means the
	// adapter itself could not run.
	Error      string
	DurationMS int64
}

// Refresher reconciles the engine's recorded state with live
// infrastructure. This is `pulumi refresh` / `terraform apply
// -refresh-only`: it reads the provider and rewrites state, and it changes
// no infrastructure.
//
// It is a state mutation, so callers must hold the stack lock for the
// duration. Only meaningful when Capabilities().SupportsRefresh; an engine
// that reports false MUST still implement the method and return an error
// saying so, so callers never have to type-assert.
type Refresher interface {
	Refresh(ctx context.Context, stack discovery.Stack, opts RefreshOpts) (RefreshResult, error)
}
