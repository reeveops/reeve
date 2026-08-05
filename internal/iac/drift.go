package iac

import (
	"context"

	"github.com/FynxLabs/reeve/internal/core/discovery"
)

// DriftChecker runs an engine-specific drift check for a single stack.
type DriftChecker interface {
	DriftCheck(ctx context.Context, stack discovery.Stack, opts PreviewOpts, refreshFirst bool) (PreviewResult, error)
}
