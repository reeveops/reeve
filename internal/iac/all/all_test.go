package all

import (
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/iac"
)

// TestAdvertisedEngineTypesResolve pins the engine.type strings that config
// docs, `reeve init`, and `reeve stacks discover --engine` advertise to the
// names the default engine set actually registers. If an advertised name
// stops resolving (e.g. a rename to/from "opentofu"), this fails.
func TestAdvertisedEngineTypesResolve(t *testing.T) {
	for _, typ := range []string{"pulumi", "terraform", "tofu"} {
		e, err := iac.New(schemas.EngineBody{Type: typ})
		if err != nil {
			t.Errorf("advertised engine type %q does not resolve via iac.New: %v", typ, err)
			continue
		}
		if e == nil {
			t.Errorf("iac.New(%q) returned a nil engine", typ)
		}
	}
	// "opentofu" is intentionally NOT a registered name - the canonical
	// spelling everywhere is "tofu".
	if _, err := iac.New(schemas.EngineBody{Type: "opentofu"}); err == nil {
		t.Error(`"opentofu" resolved; the canonical engine type is "tofu" and no alias should exist`)
	}
}
