package hcl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac"
)

// TestLiveSmoke drives a real terraform-compatible binary through the full
// lifecycle against a local backend and the builtin terraform_data resource
// (no provider downloads, no cloud creds). Skipped unless REEVE_TF_SMOKE_BIN
// points at a terraform or tofu binary:
//
//	REEVE_TF_SMOKE_BIN=$(which tofu) go test ./internal/iac/hcl/ -run TestLiveSmoke -v
func TestLiveSmoke(t *testing.T) {
	bin := os.Getenv("REEVE_TF_SMOKE_BIN")
	if bin == "" {
		t.Skip("set REEVE_TF_SMOKE_BIN to a terraform/tofu binary to run the live smoke test")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "stacks", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMain := func(rev string) {
		t.Helper()
		cfg := `terraform {
  backend "local" {}
}

resource "terraform_data" "touch" {
  input = "` + rev + `"
}
`
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMain("rev-1")

	e := New(testTerraform, schemas.EngineBody{
		Binary: schemas.EngineBinary{Path: bin},
		Stacks: []schemas.StackDecl{{Project: "demo", Path: "stacks/demo", Stacks: []string{"default", "alt"}}},
	})
	ctx := context.Background()
	stack := discovery.Stack{Project: "demo", Path: "stacks/demo", Name: "default", Env: "default"}
	opts := iac.PreviewOpts{Cwd: dir}

	// Preview: 1 create, unknown id rendered.
	prev, err := e.Preview(ctx, stack, opts)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Error != "" {
		t.Fatalf("preview error: %s", prev.Error)
	}
	if prev.Counts.Add != 1 || prev.Counts.Total() != 1 {
		t.Fatalf("preview counts: %+v", prev.Counts)
	}

	// Apply: creates the resource via the saved plan.
	app, err := e.Apply(ctx, stack, iac.ApplyOpts{Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	if app.Error != "" {
		t.Fatalf("apply error: %s\n%s", app.Error, app.Output)
	}
	if app.Counts.Add != 1 {
		t.Fatalf("apply counts: %+v", app.Counts)
	}

	// Second preview: no changes.
	prev2, err := e.Preview(ctx, stack, opts)
	if err != nil {
		t.Fatal(err)
	}
	if prev2.Error != "" || prev2.Counts.Total() != 0 {
		t.Fatalf("post-apply preview should be a no-op: err=%q counts=%+v", prev2.Error, prev2.Counts)
	}

	// Drift check: clean state, no drift, nil error.
	drift, err := e.DriftCheck(ctx, stack, opts, true)
	if err != nil {
		t.Fatalf("drift check: %v", err)
	}
	if drift.Error != "" || drift.Counts.Total() != 0 || len(drift.DriftedURNs) != 0 {
		t.Fatalf("expected clean drift check: %+v", drift)
	}

	// Config change: preview reports it without applying.
	writeMain("rev-2")
	prev3, err := e.Preview(ctx, stack, opts)
	if err != nil {
		t.Fatal(err)
	}
	if prev3.Error != "" || prev3.Counts.Total() == 0 {
		t.Fatalf("changed config must plan a change: err=%q counts=%+v", prev3.Error, prev3.Counts)
	}

	// Declared-but-missing workspace: created on first use, applies cleanly.
	alt := discovery.Stack{Project: "demo", Path: "stacks/demo", Name: "alt", Env: "alt"}
	appAlt, err := e.Apply(ctx, alt, iac.ApplyOpts{Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	if appAlt.Error != "" {
		t.Fatalf("alt workspace apply error: %s\n%s", appAlt.Error, appAlt.Output)
	}

	// Undeclared workspace: refused, never created.
	rogue := discovery.Stack{Project: "demo", Path: "stacks/demo", Name: "rogue", Env: "rogue"}
	rres, err := e.Preview(ctx, rogue, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rres.Error == "" {
		t.Fatal("undeclared workspace must be refused")
	}
}
