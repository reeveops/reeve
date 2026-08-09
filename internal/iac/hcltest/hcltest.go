// Package hcltest builds real, cloud-free fixtures for the HCL engines so
// each engine package can run the shared iac.Engine conformance suite against
// its own dialect.
//
// It lives outside package hcl because the engine packages import hcl, so hcl
// cannot import them back - and the point of the suite is to exercise the real
// terraform.Dialect and tofu.Dialect, not a copy declared in a test.
//
// Fixtures stay cloud-free and, after `init`, network-free: state goes to the
// local backend beside the source, and terraform_data is a builtin resource,
// so no provider is downloaded.
package hcltest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/iac/enginetest"
	"github.com/reeveops/reeve/internal/iac/hcl"
)

const (
	fixtureProject   = "demo"
	fixtureStackPath = "stacks/demo"
)

// RequireEnginesEnv, when set to a non-empty value, turns "engine binary
// unavailable" from a skip into a hard failure. CI sets it so the contract
// suite cannot silently stop running; a workstation without every engine
// installed still gets skips.
const RequireEnginesEnv = "REEVE_ENGINE_TESTS_REQUIRED"

// engineProbeTimeout bounds the version probe. A wedged shim, or a binary
// that blocks waiting on stdin, would otherwise hang the suite instead of
// reporting the engine as unavailable.
const engineProbeTimeout = 30 * time.Second

// ResolveBinary finds the dialect's CLI, or skips the test. An explicit
// override wins so a contributor can point at a specific build; otherwise the
// dialect's own binary name is looked up, never a sibling engine's - running
// the terraform suite against tofu would prove nothing about terraform.
// Either way the binary is probed before it is handed back.
func ResolveBinary(t *testing.T, d hcl.Dialect, overrideEnv string) string {
	t.Helper()
	var bin string
	if overrideEnv != "" {
		bin = os.Getenv(overrideEnv)
	}
	if bin == "" {
		found, err := exec.LookPath(d.Binary)
		if err != nil {
			unavailable(t, "%s not installed (set %s to override)", d.Binary, overrideEnv)
		}
		bin = found
	}
	// Resolving a path - from PATH or from an override - is not the same as
	// being runnable. A version-manager shim (mise, asdf) with no version
	// pinned for the tool resolves happily and then fails on every real
	// invocation, which turned "engine not available" into a wall of
	// contract failures blaming the adapter. An override pointing at a
	// stale or misspelled path fails the same way. Prove it runs first,
	// whichever path it came from.
	ctx, cancel := context.WithTimeout(t.Context(), engineProbeTimeout)
	defer cancel()
	// #nosec G204,G702 -- test-only helper. bin is either resolved by LookPath from the
	// dialect's own hardcoded binary name or supplied by the contributor running the suite
	// via REEVE_*_BIN; neither is reachable from .reeve config or PR content, and the only
	// argument is the literal "version". Taint analysis flags the env-var path (G702), which
	// is the point of the override - a developer naming their own build.
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		unavailable(t, "%s found at %s but is not runnable (%v): %s", d.Binary, bin, err, firstLine(out))
	}
	return bin
}

// unavailable reports that an engine cannot be exercised: a skip normally,
// a hard failure when RequireEnginesEnv is set. Callers pass the reason
// only - the outcome wording belongs to whichever branch runs, so a
// required failure never claims it was skipped.
func unavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	reason := fmt.Sprintf(format, args...)
	if os.Getenv(RequireEnginesEnv) != "" {
		t.Fatalf("%s - %s is set, so this is a failure rather than a skip", reason, RequireEnginesEnv)
	}
	t.Skipf("%s - conformance suite skipped", reason)
}

// firstLine returns the leading line of command output, trimmed. The second
// trim matters: on CRLF output the split leaves a trailing \r.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// Fixture is a real root module on disk backed by the local backend.
type Fixture struct {
	root  string
	stack discovery.Stack
	ext   string
}

// New builds an engine bound to d plus a fixture it can operate on. ext is the
// source-file extension to write, letting a dialect be exercised through the
// extension it actually reads.
func New(t *testing.T, d hcl.Dialect, bin, ext string) (iac.Engine, enginetest.Fixture) {
	t.Helper()
	root := t.TempDir()
	f := &Fixture{
		root: root,
		ext:  ext,
		stack: discovery.Stack{
			Project: fixtureProject,
			Path:    fixtureStackPath,
			Name:    hcl.DefaultWorkspace,
			Env:     hcl.DefaultWorkspace,
		},
	}
	if err := os.MkdirAll(filepath.Join(root, fixtureStackPath), 0o750); err != nil {
		t.Fatal(err)
	}
	f.Write(t, enginetest.IntentEmpty)

	e := hcl.New(d, schemas.EngineBody{
		Type:   d.TypeName,
		Binary: schemas.EngineBinary{Path: bin},
		// Declaring the stack makes enumeration authoritative: no
		// `workspace list` call, and the adapter may select it.
		Stacks: []schemas.StackDecl{{
			Project: fixtureProject,
			Path:    fixtureStackPath,
			Stacks:  []string{hcl.DefaultWorkspace},
		}},
	})
	return e, f
}

func (f *Fixture) Root() string           { return f.root }
func (f *Fixture) Stack() discovery.Stack { return f.stack }
func (f *Fixture) dir() string            { return filepath.Join(f.root, f.stack.Path) }

// Write expresses an Intent as HCL, in the fixture's configured extension.
func (f *Fixture) Write(t *testing.T, intent enginetest.Intent) {
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
		// An undeclared variable reference: rejected at plan time with no
		// plan file produced. Deliberately not a syntax error, so `init`
		// still succeeds and the failure surfaces from the plan step - the
		// path the adapter's error shaping actually runs on.
		body = "resource \"terraform_data\" \"canary\" {\n  input = var.undeclared_on_purpose\n}\n"
	default:
		t.Fatalf("unknown intent %v", intent)
	}
	if err := os.WriteFile(filepath.Join(f.dir(), "main"+f.ext), []byte(header+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// StateDigest hashes the local backend's state file. A missing file hashes to
// a stable sentinel, so the digest is comparable before and after an operation
// that legitimately creates state.
func (f *Fixture) StateDigest(t *testing.T) string {
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
