package hcl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const rootModuleTF = `terraform {
  backend "local" {}
}
`

const providerOnlyTF = `provider "random" {}

resource "random_pet" "name" {
  length = 2
}
`

const childModuleTF = `variable "name" {
  type = string
}

resource "null_resource" "x" {}
`

func TestEnumerateDeclaredStacksAuthoritative(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"envs/net/main.tf":   rootModuleTF,
		"envs/app/main.tf":   providerOnlyTF,
		"modules/vpc/vpc.tf": rootModuleTF, // under modules/: excluded even with a terraform block
	})

	fake := newFake(t, nil)
	e := New(testTerraform, schemas.EngineBody{Stacks: []schemas.StackDecl{
		{Pattern: "envs/*", Stacks: []string{"dev", "prod"}},
	}})
	e.run = fake.run

	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 stacks (2 dirs x 2 declared), got %d: %+v", len(got), got)
	}
	refs := map[string]bool{}
	for _, s := range got {
		refs[s.Ref()] = true
	}
	for _, want := range []string{"net/dev", "net/prod", "app/dev", "app/prod"} {
		if !refs[want] {
			t.Fatalf("missing %s in %v", want, refs)
		}
	}
	// Declarations are authoritative: no workspace list calls.
	if len(fake.calls) != 0 {
		t.Fatalf("declared stacks must not shell out, got %v", fake.commandLines())
	}
}

func TestEnumerateExcludesNonRootModules(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"infra/main.tf":            rootModuleTF,
		"infra/child/internal.tf":  childModuleTF, // no terraform{}/provider block: not a root module
		"modules/shared/shared.tf": rootModuleTF,  // modules/ dir: excluded
		"docs/notes.md":            "not terraform",
	})
	fake := newFake(t, nil)
	e := New(testTerraform, schemas.EngineBody{Stacks: []schemas.StackDecl{
		{Pattern: "**", Stacks: []string{"default"}},
	}})
	e.run = fake.run

	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "infra" {
		t.Fatalf("expected only infra as a root module, got %+v", got)
	}
}

func TestEnumerateWorkspaceListFallback(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"stack/main.tf": rootModuleTF})

	// workspace list fails (init never ran / binary missing): the dir
	// enumerates as project/default instead of erroring.
	fake := newFake(t, map[string]fakeResult{
		"workspace list": {exit: -1, err: errors.New("executable file not found")},
	})
	e := New(testTerraform, schemas.EngineBody{})
	e.run = fake.run

	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "default" || got[0].Project != "stack" {
		t.Fatalf("expected stack/default fallback, got %+v", got)
	}
}

func TestEnumerateWorkspaceList(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"stack/main.tf": rootModuleTF})

	fake := newFake(t, map[string]fakeResult{
		"workspace list": {stdout: "  default\n* staging\n  prod\n"},
	})
	e := New(testTerraform, schemas.EngineBody{})
	e.run = fake.run

	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(got))
	for _, s := range got {
		names = append(names, s.Name)
	}
	if len(names) != 3 || names[0] != "default" || names[1] != "prod" || names[2] != "staging" {
		t.Fatalf("workspace names off (current-workspace * marker must strip): %v", names)
	}
}

func TestEnumerateProjectNameCollision(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"envs/prod/network/main.tf":  rootModuleTF,
		"envs/stage/network/main.tf": rootModuleTF,
	})
	e := New(testTerraform, schemas.EngineBody{Stacks: []schemas.StackDecl{
		{Pattern: "envs/**", Stacks: []string{"default"}},
	}})
	e.run = newFake(t, nil).run

	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 stacks, got %+v", got)
	}
	if got[0].Project == got[1].Project {
		t.Fatalf("colliding base names must disambiguate, got %q twice", got[0].Project)
	}
}

// OpenTofu reads .tofu in addition to .tf, and where both exist for one base
// name the .tofu file wins - so a repo written for OpenTofu can contain no
// .tf at all. Matching only .tf made such a repo enumerate zero stacks with
// no error, which looks identical to "no infrastructure here".
func TestEnumerateTofuExtension(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"stacks/only-tofu/main.tofu": rootModuleTF,
	})
	decls := []schemas.StackDecl{{
		Project: "only-tofu", Path: "stacks/only-tofu", Stacks: []string{"default"},
	}}

	t.Run("tofu variant enumerates it", func(t *testing.T) {
		e := New(testTofu, schemas.EngineBody{Type: testTofu.TypeName, Stacks: decls})
		got, err := e.EnumerateStacks(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Path != "stacks/only-tofu" {
			t.Fatalf("expected the .tofu root module, got %+v", got)
		}
	})

	// The terraform CLI does not read .tofu. Enumerating it under
	// engine.type: terraform would hand back a stack that terraform then
	// refuses to plan, which is a worse failure than not finding it.
	t.Run("terraform variant does not", func(t *testing.T) {
		e := New(testTerraform, schemas.EngineBody{Type: testTerraform.TypeName, Stacks: decls})
		got, err := e.EnumerateStacks(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("terraform must not enumerate a .tofu-only module, got %+v", got)
		}
	})

	// Init-time discovery runs before an engine.type exists, so it accepts
	// both extensions; otherwise `reeve init` reports "no root modules found"
	// in a perfectly valid OpenTofu repo.
	t.Run("init scan finds it", func(t *testing.T) {
		got, err := ScanStacks(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("ScanStacks must find the .tofu root module, got %+v", got)
		}
	})
}

// A repo carrying both extensions must not enumerate the directory twice.
func TestEnumerateMixedExtensionsDeduped(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"stacks/mixed/main.tf":       rootModuleTF,
		"stacks/mixed/override.tofu": rootModuleTF,
	})
	e := New(testTofu, schemas.EngineBody{Type: testTofu.TypeName, Stacks: []schemas.StackDecl{{
		Project: "mixed", Path: "stacks/mixed", Stacks: []string{"default"},
	}}})
	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("a dir with both .tf and .tofu must enumerate once, got %+v", got)
	}
}
