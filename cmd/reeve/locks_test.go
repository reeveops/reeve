package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/audit"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
)

// lockRepo scaffolds a repo (via init) whose bucket is the local
// filesystem, and returns the lock store bound to it.
func lockRepo(t *testing.T) (string, *blocks.Store) {
	t.Helper()
	fakeTTY(t, false)
	root := pulumiRepo(t)
	if out, err := runReeve(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	fs, err := filesystem.New(filepath.Join(root, ".reeve-state"))
	if err != nil {
		t.Fatal(err)
	}
	return root, blocks.New(fs)
}

func acquire(t *testing.T, s *blocks.Store, project, stack string, pr int) {
	t.Helper()
	_, ok, err := s.TryAcquire(context.Background(), project, stack,
		corelocks.Holder{PR: pr, Actor: "alice", RunID: "r1", CommitSHA: "0000000"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("PR #%d did not acquire %s/%s", pr, project, stack)
	}
}

func enqueue(t *testing.T, s *blocks.Store, project, stack string, pr int) {
	t.Helper()
	_, ok, err := s.TryAcquire(context.Background(), project, stack,
		corelocks.Holder{PR: pr, Actor: "bob", RunID: "r2", CommitSHA: "1111111"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("PR #%d unexpectedly acquired %s/%s", pr, project, stack)
	}
}

func TestLocksListEmpty(t *testing.T) {
	lockRepo(t)
	out, err := runReeve(t, "locks", "list")
	if err != nil {
		t.Fatalf("locks list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no locks") {
		t.Errorf("expected 'no locks':\n%s", out)
	}
}

func TestLocksListShowsHolderAndQueue(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	enqueue(t, s, "proj", "dev", 8)

	out, err := runReeve(t, "locks", "list")
	if err != nil {
		t.Fatalf("locks list: %v\n%s", err, out)
	}
	for _, want := range []string{"proj/dev", "held", "PR #7", "queue=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLocksExplain(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	enqueue(t, s, "proj", "dev", 8)

	out, err := runReeve(t, "locks", "explain", "proj/dev")
	if err != nil {
		t.Fatalf("locks explain: %v\n%s", err, out)
	}
	for _, want := range []string{
		"lock: proj/dev",
		"status: held",
		"holder: PR #7",
		"actor=alice",
		"queue:",
		"1. PR #8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLocksExplainFreeLock(t *testing.T) {
	lockRepo(t)
	out, err := runReeve(t, "locks", "explain", "proj/dev")
	if err != nil {
		t.Fatalf("locks explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "holder: (free)") {
		t.Errorf("expected free holder:\n%s", out)
	}
}

func TestLocksExplainBadRef(t *testing.T) {
	lockRepo(t)
	_, err := runReeve(t, "locks", "explain", "not-a-ref")
	if err == nil || !strings.Contains(err.Error(), "expected project/stack") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocksReapExpired(t *testing.T) {
	root, _ := lockRepo(t)

	// Seed an expired holder directly: the state machine will never
	// produce one on demand.
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	lock := corelocks.Lock{
		Project: "proj", Stack: "dev",
		Holder: &corelocks.Holder{
			PR: 7, Actor: "alice", RunID: "r1",
			AcquiredAt: past, ExpiresAt: past,
		},
		Queue:     []corelocks.QueueItem{},
		UpdatedAt: past,
	}
	raw, _ := json.Marshal(lock)
	path := filepath.Join(root, ".reeve-state", "locks", "proj")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(path, "dev.json"), string(raw))

	out, err := runReeve(t, "locks", "reap")
	if err != nil {
		t.Fatalf("locks reap: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reaped 1 expired lock(s)") {
		t.Errorf("expected one reaped lock:\n%s", out)
	}
}

func TestLocksReapNothingToDo(t *testing.T) {
	lockRepo(t)
	out, err := runReeve(t, "locks", "reap")
	if err != nil {
		t.Fatalf("locks reap: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reaped 0 expired lock(s)") {
		t.Errorf("expected zero reaped:\n%s", out)
	}
}

func TestLocksUnlockRequiresStackOrPR(t *testing.T) {
	lockRepo(t)
	_, err := runReeve(t, "locks", "unlock", "--actor", "admin")
	if err == nil || !strings.Contains(err.Error(), "<project/stack> is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocksUnlockBadRef(t *testing.T) {
	lockRepo(t)
	_, err := runReeve(t, "locks", "unlock", "not-a-ref", "--actor", "admin")
	if err == nil || !strings.Contains(err.Error(), "expected project/stack") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocksUnlockForceClearsHolderAndPromotesQueue(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	enqueue(t, s, "proj", "dev", 8)

	out, err := runReeve(t, "locks", "unlock", "proj/dev", "--actor", "admin", "--reason", "stuck apply")
	if err != nil {
		t.Fatalf("locks unlock: %v\n%s", err, out)
	}
	if !strings.Contains(out, "unlocked proj/dev by actor=admin") {
		t.Errorf("missing unlock line:\n%s", out)
	}
	if !strings.Contains(out, "promoted PR #8 from queue") {
		t.Errorf("queue head not promoted:\n%s", out)
	}

	l, _, err := s.Get(context.Background(), "proj", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.PR != 8 {
		t.Errorf("holder after unlock = %+v, want PR #8", l.Holder)
	}
}

// setAdminOverride injects an admin_override block into the scaffolded
// shared.yaml's existing locking section.
func setAdminOverride(t *testing.T, root, block string) {
	t.Helper()
	path := filepath.Join(root, ".reeve", "shared.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "locking:\n"
	if !strings.Contains(string(data), anchor) {
		t.Fatal("scaffolded shared.yaml has no locking block")
	}
	updated := strings.Replace(string(data), anchor, anchor+block, 1)
	mustWrite(t, path, updated)
}

func TestLocksUnlockAdminGateRequiresReason(t *testing.T) {
	root, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	setAdminOverride(t, root, "  admin_override:\n    requires_reason: true\n")

	_, err := runReeve(t, "locks", "unlock", "proj/dev", "--actor", "admin")
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocksUnlockAdminGateActorAllowlist(t *testing.T) {
	root, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	setAdminOverride(t, root, "  admin_override:\n    allowed: [\"@alice\"]\n")

	_, err := runReeve(t, "locks", "unlock", "proj/dev", "--actor", "mallory")
	if err == nil || !strings.Contains(err.Error(), `actor "mallory" is not in locking.admin_override.allowed`) {
		t.Fatalf("err = %v", err)
	}

	// The @-prefix on either side must not matter.
	out, err := runReeve(t, "locks", "unlock", "proj/dev", "--actor", "alice")
	if err != nil {
		t.Fatalf("allowed actor rejected: %v\n%s", err, out)
	}
}

func TestLocksUnlockPRRemovesQueuedEntry(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	enqueue(t, s, "proj", "dev", 8)

	// PR-scoped removal is not admin-gated and only touches PR #8's
	// entries; PR #7 keeps holding.
	out, err := runReeve(t, "locks", "unlock", "proj/dev", "--pr", "8", "--actor", "bob")
	if err != nil {
		t.Fatalf("locks unlock --pr: %v\n%s", err, out)
	}
	if !strings.Contains(out, "removed PR #8 from proj/dev") {
		t.Errorf("missing removal line:\n%s", out)
	}
	l, _, err := s.Get(context.Background(), "proj", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.PR != 7 {
		t.Errorf("holder = %+v, want PR #7 untouched", l.Holder)
	}
	if len(l.Queue) != 0 {
		t.Errorf("queue = %+v, want empty", l.Queue)
	}
}

func TestLocksUnlockPRActiveHolderNeedsForce(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)

	_, err := runReeve(t, "locks", "unlock", "proj/dev", "--pr", "7", "--actor", "alice")
	if err == nil || !strings.Contains(err.Error(), "re-run with --force") {
		t.Fatalf("active holder should be refused without --force: %v", err)
	}

	out, err := runReeve(t, "locks", "unlock", "proj/dev", "--pr", "7", "--actor", "alice", "--force")
	if err != nil {
		t.Fatalf("--force: %v\n%s", err, out)
	}
	l, _, err := s.Get(context.Background(), "proj", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Errorf("holder = %+v, want cleared", l.Holder)
	}
}

func TestLocksUnlockPRAllStacks(t *testing.T) {
	_, s := lockRepo(t)
	acquire(t, s, "proj", "dev", 7)
	enqueue(t, s, "proj", "dev", 9)
	acquire(t, s, "proj", "prod", 9)

	// Without a stack arg, --pr sweeps every lock the PR is involved in.
	// PR #9 holds proj/prod with an active lease, so the sweep reports it
	// and asks for --force.
	_, err := runReeve(t, "locks", "unlock", "--pr", "9", "--actor", "bob")
	if err == nil || !strings.Contains(err.Error(), "proj/prod") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}

	out, err := runReeve(t, "locks", "unlock", "--pr", "9", "--actor", "bob", "--force")
	if err != nil {
		t.Fatalf("--force sweep: %v\n%s", err, out)
	}
	if !strings.Contains(out, "removed PR #9 from") {
		t.Errorf("missing sweep line:\n%s", out)
	}
	l, _, err := s.Get(context.Background(), "proj", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Errorf("proj/prod holder = %+v, want cleared", l.Holder)
	}
}

func TestActorAllowed(t *testing.T) {
	cases := []struct {
		name    string
		actor   string
		allowed []string
		want    bool
	}{
		{"plain match", "alice", []string{"alice"}, true},
		{"actor with at", "@alice", []string{"alice"}, true},
		{"allowed with at", "alice", []string{"@alice"}, true},
		{"both with at", "@alice", []string{"@alice"}, true},
		{"no match", "mallory", []string{"@alice", "@bob"}, false},
		{"empty allowed", "alice", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actorAllowed(tc.actor, tc.allowed); got != tc.want {
				t.Errorf("actorAllowed(%q, %v) = %v, want %v", tc.actor, tc.allowed, got, tc.want)
			}
		})
	}
}

func TestSplitRef(t *testing.T) {
	if got := splitRef("proj/dev"); got == nil || got[0] != "proj" || got[1] != "dev" {
		t.Errorf("splitRef(proj/dev) = %v", got)
	}
	if got := splitRef("proj/dev/extra"); got == nil || got[0] != "proj" || got[1] != "dev/extra" {
		t.Errorf("splitRef splits on the first slash only: %v", got)
	}
	if got := splitRef("nope"); got != nil {
		t.Errorf("splitRef(nope) = %v, want nil", got)
	}
}

func TestShortEtag(t *testing.T) {
	if got := shortEtag("short"); got != "short" {
		t.Errorf("shortEtag(short) = %q", got)
	}
	long := strings.Repeat("a", 20)
	if got := shortEtag(long); got != strings.Repeat("a", 12)+"…" {
		t.Errorf("shortEtag(long) = %q", got)
	}
}

// TestForceUnlockWritesAuditRecord pins the audit gate on the dangerous
// unlock shape: an admin force-unlock and a --pr --force removal (what a
// `/reeve unlock --force` comment dispatches) must record actor, stack,
// previous holder, and any promotion, and the recorded reason must pass
// through redaction.
func TestForceUnlockWritesAuditRecord(t *testing.T) {
	secretReason := "ghp_" + strings.Repeat("A", 36) + " emergency unlock"
	for _, tc := range []struct {
		name   string
		args   []string
		scope  string
		wantPR int
	}{
		{"admin force-unlock", []string{"proj/dev", "--force", "--actor", "mallory", "--reason", secretReason}, "admin", 7},
		{"pr-scoped force removal", []string{"proj/dev", "--pr", "7", "--force", "--actor", "mallory", "--reason", secretReason}, "pr", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, s := lockRepo(t)
			acquire(t, s, "proj", "dev", tc.wantPR)
			enqueue(t, s, "proj", "dev", 8)

			out, err := runReeve(t, append([]string{"locks", "unlock"}, tc.args...)...)
			if err != nil {
				t.Fatalf("locks unlock: %v\n%s", err, out)
			}

			// The override took effect: holder cleared, queue promoted.
			l, _, err := s.Get(context.Background(), "proj", "dev")
			if err != nil {
				t.Fatal(err)
			}
			if l.Holder == nil || l.Holder.PR != 8 {
				t.Fatalf("queue must be promoted after the override: %+v", l.Holder)
			}

			// The audit record exists: an intent entry written BEFORE the
			// mutation and a completion after it.
			fs, err := filesystem.New(filepath.Join(root, ".reeve-state"))
			if err != nil {
				t.Fatal(err)
			}
			keys, err := fs.List(context.Background(), "audit/")
			if err != nil {
				t.Fatal(err)
			}
			var intents, completions []audit.Entry
			for _, k := range keys {
				data, _, err := filesystem.ReadBytes(context.Background(), fs, k)
				if err != nil {
					t.Fatalf("read %s: %v", k, err)
				}
				if err != nil {
					continue
				}
				var e audit.Entry
				if err := json.Unmarshal(data, &e); err != nil {
					t.Fatalf("decode audit entry %s: %v", k, err)
				}
				if e.Op != "force_unlock" || e.LockOverride == nil {
					t.Fatalf("audit entry %s is not a force-unlock record: %+v", k, e)
				}
				if e.LockOverride.Scope != tc.scope {
					t.Fatalf("scope = %q, want %q", e.LockOverride.Scope, tc.scope)
				}
				if e.Actor != "mallory" {
					t.Fatalf("actor = %q, want mallory", e.Actor)
				}
				prev := e.LockOverride.Previous
				if len(prev) != 1 || prev[0].Project != "proj" || prev[0].Stack != "dev" ||
					prev[0].PR != tc.wantPR || prev[0].Actor != "alice" || !prev[0].Active {
					t.Fatalf("previous holder = %+v, want PR #%d by alice, active", prev, tc.wantPR)
				}
				if strings.Contains(e.LockOverride.Reason, secretReason[:8]) {
					t.Fatalf("audit reason contains the raw secret: %q", e.LockOverride.Reason)
				}
				if !strings.Contains(e.LockOverride.Reason, "[redacted]") {
					t.Fatalf("audit reason must be redacted: %q", e.LockOverride.Reason)
				}
				switch e.Outcome {
				case "force_unlock_intent":
					intents = append(intents, e)
				case "success":
					if e.LockOverride.PromotedPR != 8 {
						t.Fatalf("promoted PR = %d, want 8", e.LockOverride.PromotedPR)
					}
					completions = append(completions, e)
				}
			}
			if len(intents) != 1 || len(completions) != 1 {
				t.Fatalf("audit entries: intents=%d completions=%d, want 1 each", len(intents), len(completions))
			}
			// The intent entry was written write-once before the mutation:
			// it and the completion carry distinct run ids.
			if intents[0].RunID != completions[0].RunID+"-intent" {
				t.Fatalf("intent run id %q must pair with completion %q", intents[0].RunID, completions[0].RunID)
			}
		})
	}
}
