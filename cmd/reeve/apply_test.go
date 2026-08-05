package main

import (
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/run"
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
