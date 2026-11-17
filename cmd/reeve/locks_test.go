package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err == nil || !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "most likely still running") {
		t.Fatalf("active holder should be refused without --force, worded by the real state: %v", err)
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

// TestForceUnlockShowsHeldReason pins the operator surface: the force path
// prints the holder's recorded hold reason (outcome, run URL, audit key)
// and the validate-before-unlock instruction before clearing the lock.
func TestForceUnlockShowsHeldReason(t *testing.T) {
	root, s := lockRepo(t)
	setAdminOverride(t, root, "  admin_override:\n    allowed: [mallory]\n")
	acquire(t, s, "proj", "dev", 7)
	if err := s.RecordOutcome(context.Background(), "proj", "dev",
		corelocks.Holder{PR: 7, RunID: "r1"}, "failed",
		"https://ci.example/run/7", "audit/2026/11/12/r1.json"); err != nil {
		t.Fatal(err)
	}

	out, err := runReeve(t, "locks", "unlock", "proj/dev", "--force", "--actor", "mallory", "--reason", "state verified")
	if err != nil {
		t.Fatalf("unlock: %v\n%s", err, out)
	}
	for _, want := range []string{
		"overriding proj/dev holder PR #7",
		`outcome "failed"`,
		"https://ci.example/run/7",
		"audit/2026/11/12/r1.json",
		"validate the stack's real state",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("unlock output must surface the hold reason:\nwant: %s\ngot:  %s", want, out)
		}
	}
	l, _, err := s.Get(context.Background(), "proj", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder != nil {
		t.Fatalf("the override must still clear the holder: %+v", l.Holder)
	}
}

// seedRunDiedHold builds the ambiguous hold: the apply STARTED (persistent
// hold) but the runner is gone - lease expired, no outcome recorded. A
// renderer of this state must say: hold started, lease expired, no outcome,
// run most likely died, validate before unlocking.
func seedRunDiedHold(t *testing.T, root string, s *blocks.Store) {
	t.Helper()
	acquire(t, s, "proj", "dev", 7)
	if err := s.MarkHeld(context.Background(), "proj", "dev", corelocks.Holder{PR: 7, RunID: "r1"}); err != nil {
		t.Fatal(err)
	}
	// Rewind the lease so the hold reads as an interrupted-then-died run.
	path := filepath.Join(root, ".reeve-state", "locks", "proj", "dev.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var l corelocks.Lock
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatal(err)
	}
	l.Holder.ExpiresAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	out, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runDiedWords() []string {
	return []string{
		"hold started",
		"no outcome recorded",
		"run most likely died",
		"validate the stack's real state before unlocking",
	}
}

// TestRunDiedHoldRenderedInEverySurface pins the runner-killed hold
// rendering on each place a holder is shown: reeve locks list, explain, the
// admin force-unlock output, and the PR-scoped unlock paths (refusal without
// --force, override report with it).
func TestRunDiedHoldRenderedInEverySurface(t *testing.T) {
	root, s := lockRepo(t)
	seedRunDiedHold(t, root, s)
	setAdminOverride(t, root, "  admin_override:\n    allowed: [mallory]\n")

	// 1. reeve locks list.
	out, err := runReeve(t, "locks", "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	for _, want := range runDiedWords() {
		if !strings.Contains(out, want) {
			t.Fatalf("locks list must render the run-died state:\nwant: %s\ngot:\n%s", want, out)
		}
	}

	// 2. reve locks explain.
	out, err = runReeve(t, "locks", "explain", "proj/dev")
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	for _, want := range runDiedWords() {
		if !strings.Contains(out, want) {
			t.Fatalf("locks explain must render the run-died state:\nwant: %s\ngot:\n%s", want, out)
		}
	}

	// 3. The same removal WITHOUT --force succeeds (the lease is long
	// expired: a dead hold may be pruned) and reports the removed state.
	out, err = runReeve(t, "locks", "unlock", "proj/dev", "--pr", "7", "--actor", "mallory", "--reason", "r")
	if err != nil {
		t.Fatalf("removal: %v\n%s", err, out)
	}
	for _, want := range runDiedWords() {
		if !strings.Contains(out, want) {
			t.Fatalf("the non-forced removal must report the holder's state:\nwant: %s\ngot:\n%s", want, out)
		}
	}

	// 4. The same removal WITH --force succeeds and reports what was
	// overridden. Seed a fresh run-died hold first (3 removed it).
	seedRunDiedHold(t, root, s)
	out, err = runReeve(t, "locks", "unlock", "proj/dev", "--pr", "7", "--force", "--actor", "mallory", "--reason", "r")
	if err != nil {
		t.Fatalf("forced removal: %v\n%s", err, out)
	}
	for _, want := range runDiedWords() {
		if !strings.Contains(out, want) {
			t.Fatalf("the forced removal must report the overridden state:\nwant: %s\ngot:\n%s", want, out)
		}
	}

	// 5. The admin force-unlock path renders the recorded outcome (step 4
	// cleared the run-died hold; a fresh failed-apply hold takes its place).
	acquire(t, s, "proj", "dev", 8)
	if err := s.MarkHeld(context.Background(), "proj", "dev", corelocks.Holder{PR: 8, RunID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOutcome(context.Background(), "proj", "dev", corelocks.Holder{PR: 8, RunID: "r1"}, "failed",
		"https://ci.example/run/9", "audit/2026/11/12/r2.json"); err != nil {
		t.Fatal(err)
	}
	out, err = runReeve(t, "locks", "unlock", "proj/dev", "--force", "--actor", "mallory", "--reason", "state verified")
	if err != nil {
		t.Fatalf("admin unlock: %v\n%s", err, out)
	}
	for _, want := range []string{
		"overriding proj/dev holder PR #8",
		`recorded outcome "failed"`,
		"https://ci.example/run/9",
		"audit/2026/11/12/r2.json",
		"validate the stack's real state before unlocking",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the admin override must render the recorded outcome:\nwant: %s\ngot:\n%s", want, out)
		}
	}

	// 6. A refusal must word the refusal by the real state: a LIVE apply
	// (holder active, hold not started) is not a run-died hold.
	acquire(t, s, "proj", "dev", 9) // PR8's holder was cleared by step 5
	_, err = runReeve(t, "locks", "unlock", "proj/dev", "--pr", "9", "--actor", "mallory", "--reason", "r")
	if err == nil {
		t.Fatalf("removing a live apply's hold without --force must be refused")
	}
	if !strings.Contains(err.Error(), "most likely still running") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("the live-apply refusal must word the real state and the clearing command: %v", err)
	}
	if strings.Contains(err.Error(), "most likely died") {
		t.Fatalf("a live apply must not be worded as a run-died hold: %v", err)
	}

	// 7. A LIVE started hold (no outcome) reads "in progress" on every
	// surface - the run-died wording is reserved for an expired lease -
	// and locks explain does not print an empty outcome line.
	_, err = runReeve(t, "locks", "unlock", "proj/dev", "--pr", "9", "--force", "--actor", "mallory", "--reason", "clear")
	if err != nil {
		t.Fatalf("clear PR 9: %v\n%s", err, out)
	}
	acquire(t, s, "proj", "dev", 10)
	if err := s.MarkHeld(context.Background(), "proj", "dev", corelocks.Holder{PR: 10, RunID: "r1"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"locks", "list"},
		{"locks", "explain", "proj/dev"},
	} {
		out, err = runReeve(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		for _, want := range []string{"apply is in progress", "lease expires"} {
			if !strings.Contains(out, want) {
				t.Fatalf("a live started hold must read in progress (%v):\nwant: %s\ngot:\n%s", args, want, out)
			}
		}
		if strings.Contains(out, "most likely died") {
			t.Fatalf("a live lease must not be worded as a run death (%v):\n%s", args, out)
		}
		if strings.Contains(out, `outcome: ""`) {
			t.Fatalf("locks explain must not print an empty outcome line (%v):\n%s", args, out)
		}
	}
}
