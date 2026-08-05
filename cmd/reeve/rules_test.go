package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulesExplainScaffoldedDefaults(t *testing.T) {
	fakeTTY(t, false)
	pulumiRepo(t)
	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	out, err := runReeve(t, "rules", "explain", "projects/api/dev")
	if err != nil {
		t.Fatalf("rules explain: %v\n%s", err, out)
	}
	for _, want := range []string{
		"rules for projects/api/dev:",
		"required_approvals:",
		"require_all_groups:",
		"codeowners:",
		"dismiss_on_new_commit:",
		"approvers:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRulesExplainStackOverride(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)
	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// Tighten one stack; explain must show the merged override.
	path := filepath.Join(root, ".reeve", "shared.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "approvals:\n",
		"approvals:\n  stacks:\n    \"projects/api/prod\":\n      required_approvals: 3\n", 1)
	if updated == string(data) {
		t.Fatal("scaffold approvals block not found")
	}
	mustWrite(t, path, updated)

	out, err := runReeve(t, "rules", "explain", "projects/api/prod")
	if err != nil {
		t.Fatalf("rules explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "required_approvals:   3") {
		t.Errorf("stack override not applied:\n%s", out)
	}
}

func TestRulesExplainRequiresArg(t *testing.T) {
	fakeTTY(t, false)
	pulumiRepo(t)
	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if _, err := runReeve(t, "rules", "explain"); err == nil {
		t.Fatal("expected arg-count error")
	}
}

func TestRulesExplainWithoutConfigFails(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runReeve(t, "rules", "explain", "proj/dev"); err == nil {
		t.Fatal("expected config-load error outside a reeve repo")
	}
}
