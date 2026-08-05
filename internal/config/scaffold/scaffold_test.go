package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/core/discovery"
)

// loadRendered writes the rendered files into a fresh <tmp>/.reeve and
// round-trips them through the strict loader plus the `reeve lint` checks
// (Validate, freeze-window cron/duration parsing). Every generated
// permutation must survive this.
func loadRendered(t *testing.T, opts Options) *config.Config {
	t.Helper()
	files, err := Render(opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".reeve")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("strict load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Mirror `reeve lint`'s extra checks over freeze windows.
	for _, w := range cfg.Shared.FreezeWindows {
		if _, err := cron.ParseStandard(w.Cron); err != nil {
			t.Fatalf("freeze window %q: bad cron: %v", w.Name, err)
		}
		if w.Duration != "" {
			if _, err := time.ParseDuration(w.Duration); err != nil {
				t.Fatalf("freeze window %q: bad duration: %v", w.Name, err)
			}
		}
	}
	return cfg
}

func TestRenderBaselineNonInteractive(t *testing.T) {
	cfg := loadRendered(t, Options{})

	if got := cfg.Shared.Bucket.Type; got != "filesystem" {
		t.Errorf("bucket.type = %q, want filesystem", got)
	}
	if len(cfg.Engines) != 1 || cfg.Engines[0].Engine.Type != "pulumi" {
		t.Fatalf("want one pulumi engine, got %+v", cfg.Engines)
	}
	if len(cfg.Engines[0].Engine.Stacks) != 0 {
		t.Errorf("baseline stacks should be empty, got %+v", cfg.Engines[0].Engine.Stacks)
	}
	def := cfg.Shared.Approvals.Default
	if def.RequiredApprovals == nil || *def.RequiredApprovals != 1 {
		t.Errorf("required_approvals = %v, want 1", def.RequiredApprovals)
	}
	// Optional gates must all be OFF.
	if def.Codeowners != nil || len(def.Approvers) != 0 || def.Freshness != "" {
		t.Errorf("baseline gates not off: %+v", def)
	}
	if len(cfg.Shared.FreezeWindows) != 0 {
		t.Errorf("baseline freeze_windows should be empty, got %+v", cfg.Shared.FreezeWindows)
	}
	if cfg.Notifications != nil {
		t.Errorf("baseline should not write notifications.yaml")
	}
	if cfg.Shared.Apply.AllowForkPRs {
		t.Error("allow_fork_prs must default to false")
	}
	if cfg.Shared.Apply.Trigger != "comment" {
		t.Errorf("apply.trigger = %q, want comment", cfg.Shared.Apply.Trigger)
	}
}

func TestRenderWithStacks(t *testing.T) {
	cfg := loadRendered(t, Options{Stacks: []discovery.Declaration{
		{Pattern: "projects/*", Stacks: []string{"dev", "prod"}},
		{Project: "net", Path: "infra/net", Stacks: []string{"prod"}},
	}})
	stacks := cfg.Engines[0].Engine.Stacks
	if len(stacks) != 2 {
		t.Fatalf("want 2 stack declarations, got %+v", stacks)
	}
	if stacks[0].Pattern != "projects/*" || len(stacks[0].Stacks) != 2 {
		t.Errorf("pattern entry mangled: %+v", stacks[0])
	}
	if stacks[1].Project != "net" || stacks[1].Path != "infra/net" {
		t.Errorf("literal entry mangled: %+v", stacks[1])
	}
}

func TestRenderCodeownersGate(t *testing.T) {
	cfg := loadRendered(t, Options{ApprovalMode: ApprovalCodeowners, RequiredApprovals: 2})
	def := cfg.Shared.Approvals.Default
	if def.Codeowners == nil || !*def.Codeowners {
		t.Errorf("codeowners gate not on: %+v", def)
	}
	if def.RequiredApprovals == nil || *def.RequiredApprovals != 2 {
		t.Errorf("required_approvals = %v, want 2", def.RequiredApprovals)
	}
}

func TestRenderApproversGate(t *testing.T) {
	cfg := loadRendered(t, Options{
		ApprovalMode:      ApprovalApprovers,
		Approvers:         []string{"@org/sre", "@alice"},
		RequiredApprovals: 2,
	})
	def := cfg.Shared.Approvals.Default
	if len(def.Approvers) != 2 || def.Approvers[0] != "@org/sre" || def.Approvers[1] != "@alice" {
		t.Errorf("approvers = %v", def.Approvers)
	}
	if def.Codeowners != nil {
		t.Errorf("codeowners should be unset in approvers mode")
	}
}

func TestRenderFreshnessGate(t *testing.T) {
	cfg := loadRendered(t, Options{Freshness: "24h"})
	def := cfg.Shared.Approvals.Default
	if def.Freshness != "24h" {
		t.Errorf("freshness = %q, want 24h", def.Freshness)
	}
	if _, err := time.ParseDuration(def.Freshness); err != nil {
		t.Errorf("freshness not a duration: %v", err)
	}
}

func TestRenderFreezeExampleStaysDisabled(t *testing.T) {
	opts := Options{FreezeWindowExample: true}
	cfg := loadRendered(t, opts)
	// The example is commented, so the loaded config has no active windows.
	if len(cfg.Shared.FreezeWindows) != 0 {
		t.Errorf("freeze example must be disabled, got %+v", cfg.Shared.FreezeWindows)
	}
	files, _ := Render(opts)
	if !strings.Contains(string(files[0].Content), "# freeze_windows:") {
		t.Error("shared.yaml should carry the commented freeze_windows example")
	}
}

func TestRenderSlackGate(t *testing.T) {
	cfg := loadRendered(t, Options{SlackChannel: "#infra-deploys"})
	n := cfg.Notifications
	if n == nil {
		t.Fatal("notifications.yaml missing")
		return
	}
	if n.Version != 2 {
		t.Errorf("notifications version = %d, want 2 (channels model)", n.Version)
	}
	if n.Slack != nil {
		t.Error("must write a v2 channels: entry, not the legacy slack: block")
	}
	if len(n.Channels) != 1 || n.Channels[0].Type != "slack" {
		t.Fatalf("channels = %+v", n.Channels)
	}
	if n.Channels[0].Channel != "#infra-deploys" {
		t.Errorf("channel = %q", n.Channels[0].Channel)
	}
}

func TestRenderAllGatesOn(t *testing.T) {
	cfg := loadRendered(t, Options{
		Stacks:              []discovery.Declaration{{Pattern: "projects/*", Stacks: []string{"dev", "prod"}}},
		ApprovalMode:        ApprovalApprovers,
		Approvers:           []string{"@org/infra"},
		RequiredApprovals:   3,
		Freshness:           "48h",
		FreezeWindowExample: true,
		SlackChannel:        "#deploys",
	})
	def := cfg.Shared.Approvals.Default
	if *def.RequiredApprovals != 3 || def.Freshness != "48h" || len(def.Approvers) != 1 {
		t.Errorf("gates lost in combination: %+v", def)
	}
	if cfg.Notifications == nil || len(cfg.Notifications.Channels) != 1 {
		t.Error("slack channel lost in combination")
	}
	if len(cfg.Engines[0].Engine.Stacks) != 1 {
		t.Error("stacks lost in combination")
	}
}

// TestRenderTerraformAndTofu: each engine type renders its own
// strict-loader-clean config file with the right type, binary, and
// pre-filled stacks (workspace = stack model).
func TestRenderTerraformAndTofu(t *testing.T) {
	for _, engine := range []string{"terraform", "tofu"} {
		cfg := loadRendered(t, Options{
			EngineType: engine,
			Stacks:     []discovery.Declaration{{Pattern: "envs/*", Stacks: []string{"default"}}},
		})
		if len(cfg.Engines) != 1 || cfg.Engines[0].Engine.Type != engine {
			t.Fatalf("%s: want one %s engine, got %+v", engine, engine, cfg.Engines)
		}
		if got := cfg.Engines[0].Engine.Binary.Path; got != engine {
			t.Errorf("%s: binary.path = %q, want %q", engine, got, engine)
		}
		if len(cfg.Engines[0].Engine.Stacks) != 1 {
			t.Errorf("%s: stacks not pre-filled: %+v", engine, cfg.Engines[0].Engine.Stacks)
		}

		files, err := Render(Options{EngineType: engine})
		if err != nil {
			t.Fatal(err)
		}
		if files[1].Name != engine+".yaml" {
			t.Errorf("%s: engine file name = %q", engine, files[1].Name)
		}
	}
}

func TestOptionsValidateRejections(t *testing.T) {
	cases := []Options{
		{EngineType: "cloudformation"},
		{ApprovalMode: ApprovalApprovers}, // no approvers
		{ApprovalMode: "bogus"},
		{Freshness: "2 days"},
		{RequiredApprovals: -1},
	}
	for i, o := range cases {
		if _, err := Render(o); err == nil {
			t.Errorf("case %d (%+v): want error, got nil", i, o)
		}
	}
}

func TestExistingTypes(t *testing.T) {
	dir := t.TempDir()
	// Missing dir: empty map, no error.
	got, err := ExistingTypes(filepath.Join(dir, "nope"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing dir: got %v, %v", got, err)
	}

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("shared.yaml", "version: 1\nconfig_type: shared\n")
	write("custom-engine.yml", "version: 1\nconfig_type: engine\nengine:\n  type: pulumi\n")
	write("mystery.yaml", "just: stuff\n")
	write("notes.txt", "not yaml")

	got, err = ExistingTypes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["shared"] != "shared.yaml" {
		t.Errorf("shared -> %q", got["shared"])
	}
	if got["engine"] != "custom-engine.yml" {
		t.Errorf("engine -> %q (must find config types under unconventional names)", got["engine"])
	}
	if got["file:mystery.yaml"] != "mystery.yaml" {
		t.Errorf("headerless yaml should still register: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("unexpected extra entries: %v", got)
	}
}
