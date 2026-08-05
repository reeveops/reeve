package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacyNotifications = `version: 1
config_type: notifications
slack:
  enabled: true
  channel: "#infra"
  events: [plan, apply]
`

// migrateRepo lays out a repo with a legacy notifications.yaml and
// chdirs into it.
func migrateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".reeve", "notifications.yaml"), legacyNotifications)
	t.Chdir(root)
	return root
}

func TestMigrateConfigDryRunLeavesFilesAlone(t *testing.T) {
	root := migrateRepo(t)

	out, err := runReeve(t, "migrate-config", "--dry-run")
	if err != nil {
		t.Fatalf("migrate-config --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dry-run complete") {
		t.Errorf("missing dry-run notice:\n%s", out)
	}

	got, err := os.ReadFile(filepath.Join(root, ".reeve", "notifications.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != legacyNotifications {
		t.Error("dry-run must not modify the file")
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "notifications.yaml.bak")); !os.IsNotExist(err) {
		t.Error("dry-run must not write a backup")
	}
}

func TestMigrateConfigWritesBackupAndBumpsVersion(t *testing.T) {
	root := migrateRepo(t)

	out, err := runReeve(t, "migrate-config")
	if err != nil {
		t.Fatalf("migrate-config: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migration complete") || !strings.Contains(out, "*.bak") {
		t.Errorf("missing completion notice:\n%s", out)
	}

	bak, err := os.ReadFile(filepath.Join(root, ".reeve", "notifications.yaml.bak"))
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(bak) != legacyNotifications {
		t.Error("backup must be the pre-migration content")
	}

	migrated, err := os.ReadFile(filepath.Join(root, ".reeve", "notifications.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: 2", "channels:", "type: slack", `channel: "#infra"`, "on: [plan, apply]"} {
		if !strings.Contains(string(migrated), want) {
			t.Errorf("migrated file missing %q:\n%s", want, migrated)
		}
	}
	if strings.Contains(string(migrated), "\nslack:") {
		t.Errorf("legacy slack block should be gone:\n%s", migrated)
	}
}

func TestMigrateConfigRewritesDriftSinks(t *testing.T) {
	root := migrateRepo(t)
	driftYAML := "version: 1\nconfig_type: drift\nsinks:\n  - type: github_issue\n"
	mustWrite(t, filepath.Join(root, ".reeve", "drift.yaml"), driftYAML)

	out, err := runReeve(t, "migrate-config")
	if err != nil {
		t.Fatalf("migrate-config: %v\n%s", err, out)
	}

	migrated, err := os.ReadFile(filepath.Join(root, ".reeve", "drift.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), "channels:") || strings.Contains(string(migrated), "sinks:") {
		t.Errorf("sinks: not renamed to channels:\n%s", migrated)
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "drift.yaml.bak")); err != nil {
		t.Errorf("no backup for rewritten drift.yaml: %v", err)
	}
}

func TestMigrateConfigUpToDateIsNoOp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o750); err != nil {
		t.Fatal(err)
	}
	current := "version: 1\nconfig_type: shared\nbucket:\n  type: filesystem\n  name: ./.reeve-state\n"
	mustWrite(t, filepath.Join(root, ".reeve", "shared.yaml"), current)
	t.Chdir(root)

	out, err := runReeve(t, "migrate-config")
	if err != nil {
		t.Fatalf("migrate-config: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(root, ".reeve", "shared.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != current {
		t.Error("up-to-date file must not be rewritten")
	}
	if _, err := os.Stat(filepath.Join(root, ".reeve", "shared.yaml.bak")); !os.IsNotExist(err) {
		t.Error("no backup should be written for an unchanged file")
	}
}

func TestMigrateConfigMissingReeveDirErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runReeve(t, "migrate-config")
	if err == nil {
		t.Fatal("expected error when .reeve/ does not exist")
	}
}

func TestMigrateConfigMissingHeaderErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".reeve", "mystery.yaml"), "just: stuff\n")
	t.Chdir(root)

	_, err := runReeve(t, "migrate-config")
	if err == nil || !strings.Contains(err.Error(), "version + config_type header") {
		t.Fatalf("err = %v", err)
	}
}
