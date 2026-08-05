package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateLegacySlackToChannels(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"notifications.yaml": `version: 1
config_type: notifications
slack:
  enabled: true
  channel: "#infra-deploys"
  auth_token: xoxb-test
  trigger: plan
  events: [plan, applied, failed]
  icons:
    engine: ":pulumi:"
  rules:
    - environments: [prod]
`,
	})
	dir := filepath.Join(root, ".reeve")

	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	migrated, err := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(migrated)
	if !strings.Contains(text, "version: 2") {
		t.Fatalf("version not bumped:\n%s", text)
	}
	if strings.Contains(text, "slack:") {
		t.Fatalf("legacy slack key survived:\n%s", text)
	}
	if !strings.Contains(text, "channels:") || !strings.Contains(text, "type: slack") {
		t.Fatalf("channels list missing:\n%s", text)
	}
	if strings.Contains(text, "events:") || !strings.Contains(text, "on:") {
		t.Fatalf("events not renamed to on:\n%s", text)
	}
	// Backup of the original is kept.
	if _, err := os.Stat(filepath.Join(dir, "notifications.yaml.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	// The migrated tree loads, validates, and produces the same effective
	// channel as the legacy shape did.
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load after migrate: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate after migrate: %v", err)
	}
	channels := cfg.Notifications.Channels
	if len(channels) != 1 {
		t.Fatalf("channels: %+v", channels)
	}
	s := channels[0]
	if s.Type != "slack" || !s.IsEnabled() || s.Channel != "#infra-deploys" ||
		s.AuthToken != "xoxb-test" || string(s.Trigger) != "plan" {
		t.Fatalf("migrated channel: %+v", s)
	}
	if len(s.On) != 3 || s.On[0] != "plan" || s.On[2] != "failed" {
		t.Fatalf("on: %v", s.On)
	}
	if s.Icons == nil || s.Icons.Engine != ":pulumi:" || len(s.Rules) != 1 {
		t.Fatalf("icons/rules lost: %+v", s)
	}
}

// A header with a trailing comment and a quoted config_type is valid to the
// strict loader, so the migrator must accept it too (it used to abort the
// whole directory on this shape).
func TestMigrateHeaderWithCommentAndQuotes(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"notifications.yaml": `version: 1  # schema version
config_type: "notifications"
channels:
  - type: webhook
    url: https://example.test/hook
    on: [applied]
`,
	})
	dir := filepath.Join(root, ".reeve")
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	if !strings.Contains(string(text), "version: 2") {
		t.Fatalf("version not bumped:\n%s", text)
	}
}

func TestMigrateNotificationsWithoutSlackBumpsVersionOnly(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"notifications.yaml": `version: 1
config_type: notifications
channels:
  - type: webhook
    url: https://example.test/hook
    on: [applied]
`,
	})
	dir := filepath.Join(root, ".reeve")
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	if !strings.Contains(string(text), "version: 2") {
		t.Fatalf("version not bumped:\n%s", text)
	}
	if !strings.Contains(string(text), "type: webhook") {
		t.Fatalf("existing channels lost:\n%s", text)
	}
}

func TestMigrateMergesSlackIntoExistingChannels(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"notifications.yaml": `version: 1
config_type: notifications
slack:
  enabled: true
  channel: "#x"
channels:
  - type: webhook
    url: https://example.test/hook
    on: [applied]
`,
	})
	dir := filepath.Join(root, ".reeve")
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	s := string(text)
	if strings.Contains(s, "slack:\n") {
		t.Fatalf("slack key survived:\n%s", s)
	}
	if !strings.Contains(s, "type: webhook") || !strings.Contains(s, "type: slack") {
		t.Fatalf("merged channels wrong:\n%s", s)
	}
	if strings.Count(s, "channels:") != 1 {
		t.Fatalf("duplicate channels key:\n%s", s)
	}
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"notifications.yaml": `version: 1
config_type: notifications
slack:
  enabled: true
  channel: "#x"
`,
	})
	dir := filepath.Join(root, ".reeve")
	before, _ := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	if err := NewMigrator().MigrateDirectory(dir, true); err != nil {
		t.Fatalf("migrate --dry-run: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "notifications.yaml"))
	if string(before) != string(after) {
		t.Fatal("dry-run modified the file")
	}
	if _, err := os.Stat(filepath.Join(dir, "notifications.yaml.bak")); err == nil {
		t.Fatal("dry-run created a backup")
	}
}

func TestMigrateDriftSinksToChannels(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
		"pulumi.yaml": engineYAMLMin,
		"drift.yaml": `version: 1
config_type: drift
# where drift alerts go
sinks:
  - type: slack
    channel: "#drift"
    on: [drift_detected, drift_resolved]
`,
	})
	dir := filepath.Join(root, ".reeve")
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "drift.yaml"))
	s := string(text)
	if strings.Contains(s, "sinks:") {
		t.Fatalf("sinks key survived:\n%s", s)
	}
	if !strings.Contains(s, "channels:") || !strings.Contains(s, `channel: "#drift"`) {
		t.Fatalf("channels rename wrong:\n%s", s)
	}
	if !strings.Contains(s, "where drift alerts go") {
		t.Fatalf("comment lost:\n%s", s)
	}
	// Version stays 1 - this is a key rename, not a schema bump.
	if !strings.Contains(s, "version: 1") {
		t.Fatalf("version should stay 1:\n%s", s)
	}
	// Backup kept; migrated tree loads without the deprecation path.
	if _, err := os.Stat(filepath.Join(dir, "drift.yaml.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load after migrate: %v", err)
	}
	if len(cfg.Drift.Channels) != 1 || cfg.Drift.Channels[0].Type != "slack" {
		t.Fatalf("channels after migrate: %+v", cfg.Drift.Channels)
	}
}

func TestMigrateDriftAlreadyChannelsUntouched(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"drift.yaml": `version: 1
config_type: drift
channels:
  - type: slack
    channel: "#drift"
    on: [drift_detected]
`,
	})
	dir := filepath.Join(root, ".reeve")
	before, _ := os.ReadFile(filepath.Join(dir, "drift.yaml"))
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "drift.yaml"))
	if string(before) != string(after) {
		t.Fatal("drift.yaml with channels: should be untouched")
	}
	if _, err := os.Stat(filepath.Join(dir, "drift.yaml.bak")); err == nil {
		t.Fatal("no-op migrate created a backup")
	}
}

func TestMigrateLeavesOtherConfigTypesAlone(t *testing.T) {
	root := writeReeve(t, map[string]string{
		"shared.yaml": sharedYAMLMin,
	})
	dir := filepath.Join(root, ".reeve")
	before, _ := os.ReadFile(filepath.Join(dir, "shared.yaml"))
	if err := NewMigrator().MigrateDirectory(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "shared.yaml"))
	if string(before) != string(after) {
		t.Fatal("shared.yaml should be untouched")
	}
}
