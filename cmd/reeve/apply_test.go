package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/run"
)

func TestApplyFailedErrorNamesStacks(t *testing.T) {
	out := &run.ApplyOutput{
		RunID:        "apply-7-abc1234",
		Failed:       true,
		FailedStacks: []string{"api/prod", "worker/prod"},
	}
	err := applyFailedError(out, 18)
	if err == nil {
		t.Fatal("failed output must produce a nonzero-exit error")
	}
	for _, want := range []string{"2 stack(s)", "api/prod", "worker/prod", "PR #18", "apply-7-abc1234"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestApplyHelpDocumentsExitCodes(t *testing.T) {
	cmd := newApplyCmd()
	for _, want := range []string{"Exit codes", "0 ", "1 "} {
		if !strings.Contains(cmd.Long, want) {
			t.Fatalf("apply help missing %q:\n%s", want, cmd.Long)
		}
	}
}

// TestApplyRejectsUnknownAnnotationTypeBeforeStoreWork pins that an invalid
// annotation type is caught before the command touches the store. The
// emitters used to be built after the opportunistic lock reap and artifact
// prune, so a typo in observability.yaml did that maintenance first and
// only then failed.
func TestApplyRejectsUnknownAnnotationTypeBeforeStoreWork(t *testing.T) {
	root := driftRepo(t)
	mustWrite(t, filepath.Join(root, ".reeve", "observability.yaml"), `version: 1
config_type: observability
annotations:
  - type: graphana
    url: https://typo.example.com
`)
	_, err := runReeve(t, "run", "apply", "--pr", "1", "--sha", "abc1234", "--run-number", "1", "--repo", "acme/demo", "--token", "fake-token")
	if err == nil {
		t.Fatal("apply accepted an unknown annotation type")
	}
	if !strings.Contains(err.Error(), "graphana") {
		t.Fatalf("error should name the offending type: %v", err)
	}
	// And it must fail before the store is touched. Opening the bucket is
	// what creates this directory, and it is immediately followed by the
	// opportunistic lock reap and artifact prune - none of which should run
	// for a config that cannot be loaded.
	if _, statErr := os.Stat(filepath.Join(root, ".reeve-state")); statErr == nil {
		t.Error("apply opened the store before validating the annotation config")
	}
}
