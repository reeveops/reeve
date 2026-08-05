//go:build reeve_minimal

// Minimal-build stub for the interactive `reeve init` wizard. The real
// wizard (init_wizard.go) pulls in charmbracelet/huh and its TUI dependency
// tree; minimal builds (`-tags reeve_minimal`) exclude it per the modularity
// contract (openspec/specs/architecture: heavy dependencies are build-tag
// gated). Non-interactive init works identically in both build flavors.

package main

import (
	"errors"

	"github.com/FynxLabs/reeve/internal/config/scaffold"
	"github.com/FynxLabs/reeve/internal/core/discovery"
)

// runInitWizard is unavailable in minimal builds.
func runInitWizard(_ string, _ map[string][]discovery.Declaration) (scaffold.Options, error) {
	return scaffold.Options{}, errors.New("interactive init is not available in this build; use --non-interactive")
}
