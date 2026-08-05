package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeReeve(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".reeve")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadMinimal(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
comments:
  sort: status_grouped
  show_gates: true
`,
		"pulumi.yaml": `version: 1
config_type: engine
engine:
  type: pulumi
  binary:
    path: pulumi
  stacks:
    - project: api
      path: projects/api
      stacks: [dev, prod]
    - pattern: "services/*"
      stacks: [prod]
  filters:
    exclude:
      - "projects/sandbox/*"
      - stack: "*/scratch"
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Shared.Bucket.Type != "filesystem" {
		t.Fatalf("bucket type: %s", cfg.Shared.Bucket.Type)
	}
	if len(cfg.Engines) != 1 || cfg.Engines[0].Engine.Type != "pulumi" {
		t.Fatalf("engine not loaded: %+v", cfg.Engines)
	}
	if len(cfg.Engines[0].Engine.Filters.Exclude) != 2 {
		t.Fatalf("expected 2 exclude rules, got %d", len(cfg.Engines[0].Engine.Filters.Exclude))
	}
	if cfg.Engines[0].Engine.Filters.Exclude[0].Pattern != "projects/sandbox/*" {
		t.Fatalf("first exclude should be plain pattern: %+v", cfg.Engines[0].Engine.Filters.Exclude[0])
	}
	if cfg.Engines[0].Engine.Filters.Exclude[1].Stack != "*/scratch" {
		t.Fatalf("second exclude should be stack form: %+v", cfg.Engines[0].Engine.Filters.Exclude[1])
	}
}

func TestStackViewAndRetentionParse(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
comments:
  stack_view: changed
retention:
  max_age: 48h
`,
		"pulumi.yaml": minimalPulumi(),
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Shared.Comments.StackView != "changed" {
		t.Errorf("stack_view: got %q", cfg.Shared.Comments.StackView)
	}
	if cfg.Shared.Retention.MaxAge != "48h" {
		t.Errorf("retention.max_age: got %q", cfg.Shared.Retention.MaxAge)
	}
}

func TestChangeMappingScopeParse(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: x}
`,
		"pulumi.yaml": `version: 1
config_type: engine
engine:
  type: pulumi
  binary:
    path: pulumi
  stacks:
    - project: api
      path: projects/api
      stacks: [prod]
  change_mapping:
    scope: pulumi_only
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Engines[0].Engine.ChangeMapping.Scope != "pulumi_only" {
		t.Errorf("scope: got %q", cfg.Engines[0].Engine.ChangeMapping.Scope)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: x}
bogus_field: 42
`,
		"pulumi.yaml": minimalPulumi(),
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "bogus_field") {
		t.Fatalf("expected strict unmarshal to reject unknown field, got %v", err)
	}
}

func TestApplyCommandKeyRejected(t *testing.T) {
	// apply.command was removed; the strict loader must reject it as an
	// unknown key rather than silently ignoring a stale override.
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: x}
apply:
  trigger: comment
  command: "/reeve apply"
`,
		"pulumi.yaml": minimalPulumi(),
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected strict loader to reject apply.command, got %v", err)
	}
}

func TestApplyTriggerValidation(t *testing.T) {
	load := func(trigger string) error {
		root := writeReeve(t, map[string]string{
			"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: x}
apply:
  trigger: ` + trigger + `
`,
			"pulumi.yaml": minimalPulumi(),
		})
		cfg, err := Load(root)
		if err != nil {
			return err
		}
		return cfg.Validate()
	}
	for _, ok := range []string{"comment", "merge"} {
		if err := load(ok); err != nil {
			t.Fatalf("apply.trigger %q must be accepted, got %v", ok, err)
		}
	}
	if err := load("mrege"); err == nil || !strings.Contains(err.Error(), "apply.trigger") {
		t.Fatalf("invalid apply.trigger must be rejected, got %v", err)
	}
}

func TestDuplicateEngineTypeRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml":  minimalShared(),
		"pulumi.yaml":  minimalPulumi(),
		"pulumi2.yaml": minimalPulumi(),
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "duplicate engine.type") {
		t.Fatalf("expected duplicate engine.type error, got %v", err)
	}
}

func TestMultipleEngineConfigsRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": minimalShared(),
		"pulumi.yaml": minimalPulumi(),
		"terraform.yaml": `version: 1
config_type: engine
engine:
  type: terraform
  binary:
    path: terraform
  stacks: []
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "multiple engine configs found") {
		t.Fatalf("expected multiple-engine validate error, got %v", err)
	}
	if !strings.Contains(err.Error(), "pulumi") || !strings.Contains(err.Error(), "terraform") {
		t.Fatalf("error should name the engine types, got %v", err)
	}
}

func TestMissingEngineRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{"shared.yaml": minimalShared()})
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "engine") {
		t.Fatalf("expected validate to require engine, got %v", err)
	}
}

func TestUnsupportedVersionRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 2
config_type: shared
bucket: {type: filesystem, name: x}
`,
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected version rejection, got %v", err)
	}
}

func minimalShared() string {
	return `version: 1
config_type: shared
bucket: {type: filesystem, name: ./x}
`
}

func minimalPulumi() string {
	return `version: 1
config_type: engine
engine:
  type: pulumi
  binary: {path: pulumi}
`
}

func TestValidateApprovalSources(t *testing.T) {
	valid := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: ./x}
approvals:
  sources:
    - {type: pr_review, enabled: true}
    - {type: pr_comment, enabled: true, command: "/reeve approve"}
`,
		"pulumi.yaml": minimalPulumi(),
	})
	cfg, err := Load(valid)
	if err != nil {
		t.Fatalf("Load valid: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid sources should pass, got %v", err)
	}

	bad := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: ./x}
approvals:
  sources:
    - {type: pr_commnet, enabled: true}
`,
		"pulumi.yaml": minimalPulumi(),
	})
	cfg2, err := Load(bad)
	if err != nil {
		t.Fatalf("Load bad: %v", err)
	}
	if err := cfg2.Validate(); err == nil || !strings.Contains(err.Error(), "unknown source type") {
		t.Fatalf("typo'd source type must be rejected, got %v", err)
	}

	// A listed source with `enabled` omitted must be rejected, not silently
	// disabled (the C5 footgun).
	missingEnabled := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket: {type: filesystem, name: ./x}
approvals:
  sources:
    - {type: pr_review}
`,
		"pulumi.yaml": minimalPulumi(),
	})
	cfg3, err := Load(missingEnabled)
	if err != nil {
		t.Fatalf("Load missing-enabled: %v", err)
	}
	if err := cfg3.Validate(); err == nil || !strings.Contains(err.Error(), "must set 'enabled") {
		t.Fatalf("omitted enabled must be rejected, got %v", err)
	}
}

func TestLogSettingsNilShared(t *testing.T) {
	// The panic guard: commands call LogSettings() before Validate(), so it
	// must not dereference a nil Shared (missing .reeve/shared.yaml).
	var c *Config
	if lvl, fmtt := c.LogSettings(); lvl != "" || fmtt != "" {
		t.Fatalf("nil Config should yield empty settings, got %q/%q", lvl, fmtt)
	}
	c2 := &Config{Shared: nil}
	if lvl, fmtt := c2.LogSettings(); lvl != "" || fmtt != "" {
		t.Fatalf("nil Shared should yield empty settings, got %q/%q", lvl, fmtt)
	}
}

func TestBreakGlassBlockParses(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
break_glass:
  authorized:
    internal_list: ["alice", "myorg/sre"]
    codeowners: true
    anyone: false
    vcs_bypass: false
    groups: ["group:aws_iam:oncall"]
  override_freeze: false
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	bg := cfg.Shared.BreakGlass
	if bg == nil {
		t.Fatal("break_glass block not loaded")
		return
	}
	if len(bg.Authorized.InternalList) != 2 || !bg.Authorized.Codeowners || bg.Authorized.Anyone {
		t.Fatalf("authorized mismatch: %+v", bg.Authorized)
	}
	if len(bg.Authorized.Groups) != 1 || bg.Authorized.Groups[0] != "group:aws_iam:oncall" {
		t.Fatalf("groups mismatch: %+v", bg.Authorized.Groups)
	}
	if bg.OverrideFreeze == nil || *bg.OverrideFreeze {
		t.Fatalf("override_freeze should parse false, got %+v", bg.OverrideFreeze)
	}
}

func TestBreakGlassAbsentStaysNil(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
`,
	})
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shared.BreakGlass != nil {
		t.Fatalf("break_glass must be nil (off) when absent, got %+v", cfg.Shared.BreakGlass)
	}
}

func TestBreakGlassUnknownKeyRejected(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": `version: 1
config_type: shared
bucket:
  type: filesystem
  name: ./.reeve-state
break_glass:
  authorised:
    anyone: true
`,
	})
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "authorised") {
		t.Fatalf("strict loader must reject unknown break_glass key, got %v", err)
	}
}
