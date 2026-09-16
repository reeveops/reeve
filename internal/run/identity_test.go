package run

import "testing"

func TestRunIdentitySeparatesAttempts(t *testing.T) {
	t.Parallel()

	first := runIdentity("run", 42, 1, "abcdef1234567890")
	second := runIdentity("run", 42, 2, "abcdef1234567890")
	if first != "run-42-1-abcdef1" {
		t.Fatalf("first identity = %q", first)
	}
	if second != "run-42-2-abcdef1" {
		t.Fatalf("second identity = %q", second)
	}
	if first == second {
		t.Fatal("rerun attempts share one identity")
	}
}

func TestRunIdentityWithoutAttemptKeepsDirectCallerShape(t *testing.T) {
	t.Parallel()

	if got := runIdentity("apply", 7, 0, "abc123456789"); got != "apply-7-abc1234" {
		t.Fatalf("identity = %q", got)
	}
}
