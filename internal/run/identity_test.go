package run

import (
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
)

func TestRunIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		op         string
		runNumber  int
		runAttempt int
		sha        string
		want       string
	}{
		{name: "first attempt", op: "run", runNumber: 42, runAttempt: 1, sha: "abcdef1234567890", want: "run-42-1-abcdef1"},
		{name: "second attempt", op: "run", runNumber: 42, runAttempt: 2, sha: "abcdef1234567890", want: "run-42-2-abcdef1"},
		{name: "direct caller", op: "apply", runNumber: 7, sha: "abc123456789", want: "apply-7-abc1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := runIdentity(tt.op, tt.runNumber, tt.runAttempt, tt.sha); got != tt.want {
				t.Fatalf("runIdentity() = %q, want %q", got, tt.want)
			}
		})
	}
	if runIdentity("run", 42, 1, "abcdef1234567890") == runIdentity("run", 42, 2, "abcdef1234567890") {
		t.Fatal("rerun attempts share one artifact identity")
	}
}

func TestLockIdentityStaysStableAcrossAttempts(t *testing.T) {
	t.Parallel()
	const sha = "abcdef1234567890"
	if got := lockIdentity("apply", 42, sha); got != "apply-42-abcdef1" {
		t.Fatalf("lockIdentity() = %q, want %q", got, "apply-42-abcdef1")
	}
	if runIdentity("apply", 42, 1, sha) == runIdentity("apply", 42, 2, sha) {
		t.Fatal("artifact identities must still differ across attempts")
	}
}

func TestApplyRerunResumesStableLockIdentity(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lockStore := blocks.New(store)
	const sha = "abcdef1234567890"
	holder := corelocks.Holder{PR: 42, CommitSHA: sha, RunID: lockIdentity("apply", 7, sha)}
	if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", holder, time.Hour); err != nil || !acquired {
		t.Fatalf("attempt 1 acquire = (%t, %v), want success", acquired, err)
	}
	if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", holder, time.Hour); err != nil || !acquired {
		t.Fatalf("attempt 2 resume = (%t, %v), want idempotent success", acquired, err)
	}
	if runIdentity("apply", 7, 1, sha) == runIdentity("apply", 7, 2, sha) {
		t.Fatal("resumed lock must not collapse attempt-scoped artifact identities")
	}
}
