package terraform

import (
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/iac/enginetest"
	"github.com/reeveops/reeve/internal/iac/hcltest"
)

// Importing this package must make engine.type: terraform resolvable, since
// that import is the entire mechanism by which an engine is compiled in.
func TestRegistersTerraform(t *testing.T) {
	e, err := iac.New(schemas.EngineBody{Type: "terraform"})
	if err != nil {
		t.Fatalf("iac.New(terraform): %v", err)
	}
	if e.Name() != "Terraform" {
		t.Errorf("Name() = %q, want %q", e.Name(), "Terraform")
	}
}

// Terraform does not read .tofu. Enumerating one would hand back a stack the
// terraform CLI then refuses to plan.
func TestDialectReadsOnlyTF(t *testing.T) {
	if len(Dialect.SourceExts) != 1 || Dialect.SourceExts[0] != ".tf" {
		t.Errorf("SourceExts = %v, want [.tf] only", Dialect.SourceExts)
	}
}

// Terraform has no language-level state encryption; it is a backend concern.
// This is the capability that differs from OpenTofu, so it is asserted rather
// than left to drift back to a shared value.
func TestDialectHasNoEngineSideSecretsProvider(t *testing.T) {
	if got := Dialect.Caps.SecretsProviderTypes; len(got) != 0 {
		t.Errorf("SecretsProviderTypes = %v, want none for Terraform", got)
	}
	if !Dialect.Caps.SupportsSavedPlans || !Dialect.Caps.SupportsRefresh {
		t.Error("Terraform supports saved plans and refresh")
	}
}

// The full iac.Engine contract, driven against a real terraform binary.
func TestContract(t *testing.T) {
	bin := hcltest.ResolveBinary(t, Dialect, "REEVE_TERRAFORM_BIN")
	enginetest.RunContract(t, enginetest.Subject{
		TypeName: Dialect.TypeName,
		NewFixture: func(t *testing.T) (iac.Engine, enginetest.Fixture) {
			return hcltest.New(t, Dialect, bin, ".tf")
		},
	})
}
