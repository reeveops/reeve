package run

import (
	"errors"
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
		{name: "first attempt", op: "run", runNumber: 42, runAttempt: 1, sha: "abcdef1234567890", want: "run-42-1-abcdef1234567890"},
		{name: "second attempt", op: "run", runNumber: 42, runAttempt: 2, sha: "abcdef1234567890", want: "run-42-2-abcdef1234567890"},
		{name: "direct caller", op: "apply", runNumber: 7, sha: "abc123456789", want: "apply-7-abc123456789"},
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
	if runIdentity("run", 42, 1, "abcdef1aaaaaaaaa") == runIdentity("run", 42, 1, "abcdef1bbbbbbbbb") {
		t.Fatal("commits with the same short SHA share one artifact identity")
	}
}

func TestApplyRerunWaitsForEarlierAttemptLease(t *testing.T) {
	t.Parallel()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lockStore := blocks.New(store)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	lockStore.Now = func() time.Time { return now }
	const sha = "abcdef1234567890"
	holder := corelocks.Holder{PR: 42, CommitSHA: sha, RunID: runIdentity("apply", 7, 1, sha)}
	if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", holder, time.Hour); err != nil || !acquired {
		t.Fatalf("attempt 1 acquire = (%t, %v), want success", acquired, err)
	}
	retry := corelocks.Holder{PR: 42, CommitSHA: sha, RunID: runIdentity("apply", 7, 2, sha)}
	if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", retry, time.Hour); !errors.Is(err, corelocks.ErrHeldBySamePR) || acquired {
		t.Fatalf("attempt 2 live-lease acquire = (%t, %v), want %v", acquired, err, corelocks.ErrHeldBySamePR)
	}
	lockStore.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", retry, time.Hour); err != nil || !acquired {
		t.Fatalf("attempt 2 expired-lease acquire = (%t, %v), want success", acquired, err)
	}
}
