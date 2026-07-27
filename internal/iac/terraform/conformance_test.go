package terraform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/iac/enginetest"
)

// The conformance suite runs against a REAL terraform/OpenTofu binary: the
// adapter builds real argv, execs a real process, and parses real
// `show -json` output. Nothing about the engine is faked.
//
// It stays cloud-free and network-free after `init`: the local backend keeps
// state in the stack directory, and `terraform_data` is a builtin resource,
// so no provider is ever downloaded.
//
// Binary resolution, in order: $REEVE_TF_SMOKE_BIN, then `tofu`, then
// `terraform` on PATH. With none of those present the suite skips - CI
// installs OpenTofu so it always runs there.

func TestContract_OpenTofu(t *testing.T) {
	bin, variant := resolveEngineBinary(t)
	enginetest.RunContract(t, enginetest.Subject{
		TypeName: variant.TypeName,
		NewFixture: func(t *testing.T) (iac.Engine, enginetest.Fixture) {
			return newTFFixture(t, bin, variant)
		},
	})
}

// resolveEngineBinary finds an installed terraform-compatible CLI, or skips.
func resolveEngineBinary(t *testing.T) (string, Variant) {
	t.Helper()
	if bin := os.Getenv("REEVE_TF_SMOKE_BIN"); bin != "" {
		v := OpenTofu
		if filepath.Base(bin) == "terraform" {
			v = Terraform
		}
		return bin, v
	}
	for _, c := range []struct {
		name string
		v    Variant
	}{{"tofu", OpenTofu}, {"terraform", Terraform}} {
		if p, err := exec.LookPath(c.name); err == nil {
			return p, c.v
		}
	}
	t.Skip("no terraform/tofu binary found (set REEVE_TF_SMOKE_BIN or install one) - conformance suite skipped")
	return "", Variant{}
}

const (
	fixtureProject   = "demo"
	fixtureStackPath = "stacks/demo"
)

// tfFixture is a real root module on disk backed by the local backend.
type tfFixture struct {
	root  string
	stack discovery.Stack
}

func newTFFixture(t *testing.T, bin string, v Variant) (iac.Engine, enginetest.Fixture) {
	t.Helper()
	root := t.TempDir()
	f := &tfFixture{
		root: root,
		stack: discovery.Stack{
			Project: fixtureProject,
			Path:    fixtureStackPath,
			Name:    defaultWorkspace,
			Env:     defaultWorkspace,
		},
	}
	if err := os.MkdirAll(filepath.Join(root, fixtureStackPath), 0o750); err != nil {
		t.Fatal(err)
	}
	f.Write(t, enginetest.IntentEmpty)

	e := New(v, schemas.EngineBody{
		Type:   v.TypeName,
		Binary: schemas.EngineBinary{Path: bin},
		// Declaring the stack makes enumeration authoritative: no
		// `workspace list` call, and the adapter is allowed to select it.
		Stacks: []schemas.StackDecl{{
			Project: fixtureProject,
			Path:    fixtureStackPath,
			Stacks:  []string{defaultWorkspace},
		}},
	})
	return e, f
}

func (f *tfFixture) Root() string           { return f.root }
func (f *tfFixture) Stack() discovery.Stack { return f.stack }

func (f *tfFixture) dir() string { return filepath.Join(f.root, f.stack.Path) }

// Write expresses an Intent as HCL. The local backend keeps state beside the
// source, so a fixture is entirely self-contained in its temp dir.
func (f *tfFixture) Write(t *testing.T, intent enginetest.Intent) {
	t.Helper()
	const header = "terraform {\n  backend \"local\" {}\n}\n\n"
	var body string
	switch intent {
	case enginetest.IntentEmpty:
		body = ""
	case enginetest.IntentOne:
		body = "resource \"terraform_data\" \"canary\" {\n  input = \"v1\"\n}\n"
	case enginetest.IntentOneChanged:
		body = "resource \"terraform_data\" \"canary\" {\n  input = \"v2\"\n}\n"
	case enginetest.IntentBroken:
		// An undeclared variable reference: the engine rejects it at plan
		// time, with no plan file produced. Deliberately NOT a syntax error,
		// so `init` still succeeds and the failure surfaces from the plan
		// step - the path the adapter's error shaping actually runs on.
		body = "resource \"terraform_data\" \"canary\" {\n  input = var.undeclared_on_purpose\n}\n"
	default:
		t.Fatalf("unknown intent %v", intent)
	}
	path := filepath.Join(f.dir(), "main.tf")
	if err := os.WriteFile(path, []byte(header+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// StateDigest hashes the local backend's state file. A missing file hashes
// to a stable sentinel rather than failing, so the digest is comparable
// before and after an operation that legitimately creates state.
func (f *tfFixture) StateDigest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir(), "terraform.tfstate"))
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
