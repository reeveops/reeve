package pulumi

import (
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/iac"
)

// TestRegistryResolvesPulumi exercises the init() self-registration: the
// factory resolves engine.type "pulumi" to this adapter and passes the
// config's binary path through.
func TestRegistryResolvesPulumi(t *testing.T) {
	e, err := iac.New(schemas.EngineBody{
		Type:   "pulumi",
		Binary: schemas.EngineBinary{Path: "/custom/pulumi"},
	})
	if err != nil {
		t.Fatalf("iac.New: %v", err)
	}
	if e.Name() != "pulumi" {
		t.Errorf("Name() = %q, want pulumi", e.Name())
	}
	pe, ok := e.(*Engine)
	if !ok {
		t.Fatalf("iac.New returned %T, want *pulumi.Engine", e)
	}
	if pe.Binary != "/custom/pulumi" {
		t.Errorf("Binary = %q, want /custom/pulumi", pe.Binary)
	}
}

// TestRegistryDefaultBinary: an empty binary path keeps the "pulumi" default.
func TestRegistryDefaultBinary(t *testing.T) {
	e, err := iac.New(schemas.EngineBody{Type: "pulumi"})
	if err != nil {
		t.Fatalf("iac.New: %v", err)
	}
	if pe := e.(*Engine); pe.Binary != "pulumi" {
		t.Errorf("Binary = %q, want pulumi", pe.Binary)
	}
}
