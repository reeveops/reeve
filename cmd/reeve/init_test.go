package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/config"
	"github.com/reeveops/reeve/internal/config/schemas"
)

// fakeTTY overrides the injected TTY probe for one test.
func fakeTTY(t *testing.T, isTTY bool) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return isTTY }
	t.Cleanup(func() { stdinIsTTY = orig })
}

// pulumiRepo lays out a minimal repo with two Pulumi projects and chdirs
// into it.
func pulumiRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"api", "web"} {
		dir := filepath.Join(root, "projects", p)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, "Pulumi.yaml"), "name: "+p+"\nruntime: yaml\n")
		mustWrite(t, filepath.Join(dir, "Pulumi.dev.yaml"), "")
		mustWrite(t, filepath.Join(dir, "Pulumi.prod.yaml"), "")
	}
	t.Chdir(root)
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runReeve executes the root command with args and returns combined output.
func runReeve(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestInitNonInteractiveScaffolds(t *testing.T) {
	fakeTTY(t, true) // TTY present, but -n forces non-interactive
	root := pulumiRepo(t)

	out, err := runReeve(t, "init", "-n")
	if err != nil {
		t.Fatalf("init -n: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Discovered 4 Pulumi stack(s)",
		"wrote   .reeve/shared.yaml",
		"wrote   .reeve/pulumi.yaml",
		"reeve lint",
		"reeveops/reeve/.github/workflows/reeve.yml@<full-commit-sha>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "notifications.yaml")); !os.IsNotExist(err) {
		t.Error("non-interactive init must not write notifications.yaml")
	}

	// Round-trip the written tree through the strict loader (lint's core).
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("strict load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(cfg.Engines[0].Engine.Stacks) != 1 || cfg.Engines[0].Engine.Stacks[0].Pattern != "projects/*" {
		t.Errorf("discovered stacks not pre-filled: %+v", cfg.Engines[0].Engine.Stacks)
	}
}

func TestInitWritesPinnedWorkflowForReleaseBuild(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)
	originalCommit := commit
	commit = "0123456789abcdef0123456789abcdef01234567"
	t.Cleanup(func() { commit = originalCommit })

	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "wrote   .github/workflows/reeve.yml") {
		t.Fatalf("workflow write not reported:\n%s", out)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "reeve.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"reeve.yml@0123456789abcdef0123456789abcdef01234567",
		"merge_group:",
		"types: [checks_requested]",
		"mode: gitops",
		"pulumi_version: latest",
	} {
		if !strings.Contains(string(workflow), want) {
			t.Errorf("workflow missing %q:\n%s", want, workflow)
		}
	}
}

func TestConfigNeedsOIDC(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  string
		want bool
	}{
		{name: "none"},
		{name: "AWS OIDC", typ: "aws_oidc", want: true},
		{name: "GCP WIF", typ: "gcp_wif", want: true},
		{name: "Azure federation", typ: "azure_federated", want: true},
		{name: "GitHub App", typ: "github_app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			if tc.typ != "" {
				cfg.Auth = &schemas.Auth{Providers: map[string]schemas.ProviderYAML{
					"provider": {Type: tc.typ},
				}}
			}
			if got := configNeedsOIDC(cfg); got != tc.want {
				t.Fatalf("configNeedsOIDC() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInitWorkflowMatchesPreservedConfig(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)
	if out, err := runReeve(t, "init", "-n"); err != nil {
		t.Fatalf("baseline init: %v\n%s", err, out)
	}

	sharedPath := filepath.Join(root, ".reeve", "shared.yaml")
	shared, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	shared = []byte(strings.Replace(string(shared), "trigger: comment", "trigger: merge", 1))
	mustWrite(t, sharedPath, string(shared))
	mustWrite(t, filepath.Join(root, ".reeve", "auth.yaml"), `version: 1
config_type: auth
providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111111111111:role/reeve-prod
bindings:
  - match: {stack: "*"}
    providers: [aws-prod]
`)

	const ref = "0123456789abcdef0123456789abcdef01234567"
	out, err := runReeve(t, "init", "-n", "--workflow-ref", ref)
	if err != nil {
		t.Fatalf("init with existing config: %v\n%s", err, out)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "reeve.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ready_for_review, closed", "id-token: write"} {
		if !strings.Contains(string(workflow), want) {
			t.Errorf("workflow missing %q:\n%s", want, workflow)
		}
	}
}

func TestInitWorkflowRefSelectsDetectedEngineAndPreservesExisting(t *testing.T) {
	fakeTTY(t, false)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.tf"), "terraform {}\n")
	t.Chdir(root)
	const ref = "89abcdef0123456789abcdef0123456789abcdef"

	out, err := runReeve(t, "init", "--workflow-ref", ref)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	workflowPath := filepath.Join(root, ".github", "workflows", "reeve.yml")
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "terraform_version: latest") {
		t.Fatalf("terraform workflow input missing:\n%s", workflow)
	}

	mustWrite(t, workflowPath, "name: custom\n")
	out, err = runReeve(t, "init", "--workflow-ref", ref, "--force")
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	workflow, err = os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(workflow) != "name: custom\n" {
		t.Fatalf("existing workflow was overwritten:\n%s", workflow)
	}
	if !strings.Contains(out, "existing workflow is never overwritten") {
		t.Errorf("preserved workflow not reported:\n%s", out)
	}
}

func TestInitRejectsUnpinnedWorkflowRefBeforeWriting(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)

	if out, err := runReeve(t, "init", "--workflow-ref", "master"); err == nil {
		t.Fatalf("want error for moving workflow ref:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve")); !os.IsNotExist(err) {
		t.Fatalf("invalid ref wrote config: %v", err)
	}
}

func TestInitWorkflowWriteRejectsSymlinkedGitHubDirectory(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".github")); err != nil {
		t.Fatal(err)
	}

	const ref = "0123456789abcdef0123456789abcdef01234567"
	if out, err := runReeve(t, "init", "--workflow-ref", ref); err == nil {
		t.Fatalf("want error for symlinked .github directory:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(outside, "workflows", "reeve.yml")); !os.IsNotExist(err) {
		t.Fatalf("workflow escaped repository root: %v", err)
	}
}

func TestInitAutoSelectsNonInteractiveWithoutTTY(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)

	// No -n flag: the missing TTY must select non-interactive mode (a wizard
	// launch here would hang or error).
	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("init (no tty): %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "shared.yaml")); err != nil {
		t.Errorf("shared.yaml not written: %v", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)

	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}
	before, err := os.ReadFile(filepath.Join(root, ".reeve", "shared.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to do") {
		t.Errorf("second run should be a no-op:\n%s", out)
	}
	after, err := os.ReadFile(filepath.Join(root, ".reeve", "shared.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("second init modified shared.yaml without --force")
	}
}

func TestInitFillsOnlyMissingTypes(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)

	// Pre-existing shared config under an unconventional name.
	custom := "version: 1\nconfig_type: shared\nbucket:\n  type: s3\n  name: my-bucket\n"
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".reeve", "main.yaml"), custom)

	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kept    .reeve/main.yaml") {
		t.Errorf("existing shared config not reported as kept:\n%s", out)
	}
	if !strings.Contains(out, "wrote   .reeve/pulumi.yaml") {
		t.Errorf("missing engine config not filled in:\n%s", out)
	}
	got, err := os.ReadFile(filepath.Join(root, ".reeve", "main.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Error("pre-existing shared config was modified")
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "shared.yaml")); !os.IsNotExist(err) {
		t.Error("init wrote a duplicate shared config alongside main.yaml")
	}
	// The merged tree must still pass the strict loader.
	if _, err := config.Load(root); err != nil {
		t.Fatalf("strict load after fill: %v", err)
	}
}

func TestInitForceRegeneratesWithBackup(t *testing.T) {
	fakeTTY(t, false)
	root := pulumiRepo(t)

	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}
	shared := filepath.Join(root, ".reeve", "shared.yaml")
	mustWrite(t, shared, "version: 1\nconfig_type: shared\nbucket:\n  type: s3\n  name: edited\n")

	out, err := runReeve(t, "init", "--force")
	if err != nil {
		t.Fatalf("init --force: %v\n%s", err, out)
	}
	bak, err := os.ReadFile(shared + ".bak")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if !strings.Contains(string(bak), "edited") {
		t.Error("backup does not contain the pre-force content")
	}
	regenerated, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(regenerated), "edited") {
		t.Error("--force did not regenerate shared.yaml")
	}
	if _, err := config.Load(root); err != nil {
		t.Fatalf("strict load after force: %v", err)
	}
}

// TestInitDetectsTerraform: a repo with terraform root modules and no
// Pulumi projects scaffolds a terraform engine config with the discovered
// stacks pre-filled (workspace model: <project>/default). OpenTofu is never
// auto-picked.
func TestInitDetectsTerraform(t *testing.T) {
	fakeTTY(t, false)
	root := t.TempDir()
	for _, p := range []string{"net", "app"} {
		dir := filepath.Join(root, "envs", p)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, "main.tf"), "terraform {\n  backend \"local\" {}\n}\n")
	}
	t.Chdir(root)

	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Discovered 2 Terraform stack(s)",
		"terraform root modules detected",
		"wrote   .reeve/terraform.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("strict load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	eng := cfg.Engines[0].Engine
	if eng.Type != "terraform" {
		t.Fatalf("engine.type = %q, want terraform (tofu must never be auto-picked)", eng.Type)
	}
	if len(eng.Stacks) == 0 {
		t.Error("terraform stacks not pre-filled")
	}
}

func TestInitEmptyRepoStillWritesBaseline(t *testing.T) {
	fakeTTY(t, false)
	root := t.TempDir()
	t.Chdir(root)

	out, err := runReeve(t, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no Pulumi projects or Terraform root modules found") {
		t.Errorf("expected empty-repo note:\n%s", out)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("strict load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(cfg.Engines[0].Engine.Stacks) != 0 {
		t.Errorf("expected empty stacks, got %+v", cfg.Engines[0].Engine.Stacks)
	}
}

func TestInitHelpMentionsModes(t *testing.T) {
	out, err := runReeve(t, "init", "--help")
	if err != nil {
		t.Fatalf("init --help: %v", err)
	}
	for _, want := range []string{"--non-interactive", "--force", "--workflow-ref", "wizard", "*.bak", "reeve lint"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
