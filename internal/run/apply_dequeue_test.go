package run

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/summary"
)

// seedQueuedStack installs holder pr holding api/prod and queues pr
// behind it, mirroring the FIFO the lock store maintains for PRs that
// arrive while another apply runs.
func seedQueuedStack(t *testing.T, s *blocks.Store, holderPR int, queued []int) {
	t.Helper()
	ctx := context.Background()
	const seedTTL = time.Hour
	if _, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: holderPR, RunID: "holder-run", Actor: "holder"}, seedTTL); err != nil || !ok {
		t.Fatalf("seed holder acquire: ok=%v err=%v", ok, err)
	}
	for _, pr := range queued {
		if _, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: pr, RunID: "next-run", Actor: "next"}, seedTTL); err != nil || ok {
			t.Fatalf("seed queue %d: ok=%v err=%v", pr, ok, err)
		}
	}
}

// applyQueueFixture builds a blocked-apply fixture over one stack whose
// lock is held by PR 501 with PR 19 waiting behind it.
func applyQueueFixture(t *testing.T, fv *bgVCS, approvalsGreen bool) (ApplyInput, *blocks.Store) {
	t.Helper()
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	return queueFixtureEngine(t, engine, fv, approvalsGreen)
}

func queueFixtureEngine(t *testing.T, engine *bgEngine, fv *bgVCS, approvalsGreen bool) (ApplyInput, *blocks.Store) {
	t.Helper()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ls := blocks.New(blobStore)
	seedQueuedStack(t, ls, 501, []int{19})
	in := plainApplyInput(t, engine, fv, blobStore)
	if !approvalsGreen {
		fv.approvalsList = nil // the approvals gate fails while queued
	}
	return in, ls
}

// TestGateBlockedPrLeavesQueue pins the FIFO hygiene fix: a PR whose apply
// queued for a held lock and then failed a NON-lock gate (approvals here)
// must not keep its queue entry. A ghost reservation would be promoted when
// the holder releases and would hold the lock for a run that can never
// pass its gates. The test asserts the denial AND the reason: the run is
// blocked, the queue entry is gone, and the gate-blocked comment carries
// the removal with the re-apply step; the PR that legitimately waits
// (PR 19) is promoted when the holder releases.
func TestGateBlockedPrLeavesQueue(t *testing.T) {
	ctx := context.Background()

	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in, ls := applyQueueFixture(t, fv, false)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("gate-blocked run must be blocked: %+v", out)
	}
	var ss summary.StackSummary
	for _, s := range out.Stacks {
		if s.Ref() == "api/prod" {
			ss = s
		}
	}
	if ss.Status != summary.StatusBlocked {
		t.Fatalf("status = %q, want blocked", ss.Status)
	}

	// PR 18 must be out of the queue across ALL of its run ids (today's
	// run and the stale earlier run); PR 19 keeps its place.
	l, _, err := ls.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	var stale18 []string
	for _, q := range l.Queue {
		if q.PR == 18 {
			stale18 = append(stale18, q.RunID)
		}
	}
	if len(stale18) != 0 || l.Holder == nil || l.Holder.PR != 501 {
		t.Fatalf("queue after dequeue (stale entries: %v): holder=%+v queue=%+v", stale18, l.Holder, l.Queue)
	}

	// The gate-blocked comment must say the queue entry was removed and
	// what to do next.
	if want := "Removed from the lock queue for api/prod; re-run `/reeve apply` once the gates pass."; !strings.Contains(fv.allComments(), want) {
		t.Fatalf("gate-blocked comment must carry the dequeue note:\nwant: %s\ncomments:\n%s", want, fv.allComments())
	}

	// The FIFO must still work for the PR that legitimately waits: when the
	// holder releases, the NEXT queued PR is promoted.
	if _, err := ls.Release(ctx, "api", "prod", 501, "holder-run", 0); err != nil {
		t.Fatalf("holder release: %v", err)
	}
	l, _, _ = ls.Get(ctx, "api", "prod")
	if l.Holder == nil || l.Holder.PR != 19 {
		t.Fatalf("next queued PR must be promoted on release: holder=%+v", l.Holder)
	}
}

// TestLockBlockedPrStaysQueued is the control for the dequeue rule: blocked
// BY the lock (no other gate failing) is the deliberate queue-the-apply
// flow, so the queue entries stay and nothing announces a removal.
func TestLockBlockedPrStaysQueued(t *testing.T) {
	ctx := context.Background()

	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in, ls := applyQueueFixture(t, fv, true)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("lock-blocked run must be blocked: %+v", out)
	}

	l, _, err := ls.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	var queued int
	for _, q := range l.Queue {
		if q.PR == 18 || q.PR == 19 {
			queued++
		}
	}
	var blockedStack summary.StackSummary
	for _, s := range out.Stacks {
		if s.Ref() == "api/prod" {
			blockedStack = s
		}
	}
	if queued != 2 || l.Holder == nil || l.Holder.PR != 501 {
		t.Fatalf("deliberate queue entries must stay: holder=%+v queue=%+v gates=%+v comments=%s", l.Holder, l.Queue, blockedStack.Gates, fv.allComments())
	}
	if strings.Contains(fv.allComments(), "Removed from the lock queue") {
		t.Fatalf("lock-blocked queue entries are deliberate; no removal note expected:\n%s", fv.allComments())
	}
}

// TestDequeueSweepsStaleRunEntries proves the run-id scoping the dequeue
// relies on: UnlockPR removes a PR's queue entries regardless of which run
// enqueued them, so an entry left behind by an earlier run (a crashed run
// between enqueue and dequeue) is swept by a later removal for the same PR.
func TestDequeueSweepsStaleRunEntries(t *testing.T) {
	ctx := context.Background()
	_, ls := applyQueueFixture(t, &bgVCS{}, true)

	// Seed PR 18's entries directly (a store can only hold ONE queue entry
	// per PR: TryAcquire refuses an already-queued PR), then prove a
	// later run's removal sweeps it regardless of the enqueuing run id.
	if _, ok, err := ls.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 18, RunID: "apply-1-older", Actor: "alice"}, time.Hour); err != nil || ok {
		t.Fatalf("seed stale queue entry: ok=%v err=%v", ok, err)
	}

	// The current run's dequeue (run id apply-9) must remove both stale
	// entries: the sweep keys on the PR, not the run id.
	if _, err := ls.UnlockPR(ctx, "api", "prod", 18, "apply-9", time.Hour, false); err != nil {
		t.Fatalf("UnlockPR: %v", err)
	}
	l, _, err := ls.Get(ctx, "api", "prod")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range l.Queue {
		if q.PR == 18 {
			t.Fatalf("stale entry for run %q survived the dequeue: %+v", q.RunID, l.Queue)
		}
	}
	// PR 19 (a different PR) keeps its entry.
	found19 := false
	for _, q := range l.Queue {
		if q.PR == 19 {
			found19 = true
		}
	}
	if !found19 {
		t.Fatalf("another PR's queue entry must survive the sweep: %+v", l.Queue)
	}
}
