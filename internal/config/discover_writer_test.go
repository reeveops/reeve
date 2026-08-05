package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/discovery"
)

const original = `version: 1
config_type: engine

# Pulumi engine - default for the monorepo.
engine:
  type: pulumi
  binary:
    path: pulumi
  # Stacks block gets rewritten by ` + "`" + `reeve stacks discover --write` + "`" + `.
  stacks:
    - project: oldthing
      path: projects/oldthing
      stacks: [dev]
  # Exclude rules - keep this comment.
  filters:
    exclude:
      - "projects/sandbox/**"
  execution:
    max_parallel_stacks: 4
`

func TestWriteClusteredStacks_PreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulumi.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	decls := []discovery.Declaration{
		{Pattern: "projects/*", Stacks: []string{"dev", "prod"}},
		{Project: "payments", Path: "projects/payments", Stacks: []string{"prod"}},
	}
	out, err := WriteClusteredStacks(path, decls, false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// Sibling comments preserved.
	for _, want := range []string{
		"# Pulumi engine - default for the monorepo.",
		"# Exclude rules - keep this comment.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected preserved comment %q, got:\n%s", want, got)
		}
	}
	// New stacks replaced.
	if !strings.Contains(got, `pattern: "projects/*"`) {
		t.Fatalf("expected new pattern entry; got:\n%s", got)
	}
	if strings.Contains(got, "oldthing") {
		t.Fatalf("old stack block should be gone; got:\n%s", got)
	}
	// Backup written.
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected backup %s: %v", path+".bak", err)
	}
}

func TestWriteClusteredStacks_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulumi.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	decls := []discovery.Declaration{{Pattern: "new/*", Stacks: []string{"prod"}}}
	out, err := WriteClusteredStacks(path, decls, true)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("dry-run should not modify file")
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Fatal("dry-run should not create .bak")
	}
	if !strings.Contains(string(out), "new/*") {
		t.Fatal("dry-run output should contain new pattern")
	}
}
