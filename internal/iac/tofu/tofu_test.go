package tofu

import (
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/iac/enginetest"
	"github.com/reeveops/reeve/internal/iac/hcltest"
)

func TestRegistersTofu(t *testing.T) {
	e, err := iac.New(schemas.EngineBody{Type: "tofu"})
	if err != nil {
		t.Fatalf("iac.New(tofu): %v", err)
	}
	if e.Name() != "OpenTofu" {
		t.Errorf("Name() = %q, want %q", e.Name(), "OpenTofu")
	}
}

// OpenTofu reads .tofu as well as .tf, and prefers .tofu where both exist.
func TestDialectReadsTofuExtension(t *testing.T) {
	var tf, tofu bool
	for _, e := range Dialect.SourceExts {
		switch e {
		case ".tf":
			tf = true
		case ".tofu":
			tofu = true
		}
	}
	if !tf || !tofu {
		t.Errorf("SourceExts = %v, want both .tf and .tofu", Dialect.SourceExts)
	}
}

// OpenTofu encrypts state in the language, unlike Terraform. The shared
// adapter used to report nil here for both engines, which told callers
// OpenTofu had no engine-side secrets provider - simply wrong.
func TestDialectReportsStateEncryption(t *testing.T) {
	if len(Dialect.Caps.SecretsProviderTypes) == 0 {
		t.Error("OpenTofu configures state encryption in the language; " +
			"SecretsProviderTypes must not be empty")
	}
}

// The full iac.Engine contract, driven against a real tofu binary. Running
// this separately from the terraform subject is the point: one suite, two
// engines, each through the binary and extension it actually uses.
func TestContract(t *testing.T) {
	bin := hcltest.ResolveBinary(t, Dialect, "REEVE_TOFU_BIN")
	enginetest.RunContract(t, enginetest.Subject{
		TypeName: Dialect.TypeName,
		NewFixture: func(t *testing.T) (iac.Engine, enginetest.Fixture) {
			// Exercised through .tofu, the extension only this engine reads.
			return hcltest.New(t, Dialect, bin, ".tofu")
		},
	})
}
