package locks

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

func TestAcquireFree(t *testing.T) {
	l := NewLock("api", "prod", t0)
	got, ok, err := TryAcquire(l, Holder{PR: 1, CommitSHA: "aaa"}, time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Holder == nil || got.Holder.PR != 1 {
		t.Fatalf("expected held by pr 1, got %+v", got)
	}
}

func TestAcquireAlreadyHeldEnqueues(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	l, ok, err := TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	if err != nil || ok {
		t.Fatalf("expected queued, got ok=%v err=%v", ok, err)
	}
	if len(l.Queue) != 1 || l.Queue[0].PR != 2 {
		t.Fatalf("queue wrong: %+v", l.Queue)
	}
}

func TestAcquireIdempotent(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	l, ok, err := TryAcquire(l, Holder{PR: 1, RunID: "run-a", CommitSHA: "bbb"}, time.Hour, t0.Add(10*time.Minute))
	if !ok || !errors.Is(err, ErrAlreadyHolder) {
		t.Fatalf("expected ErrAlreadyHolder, got ok=%v err=%v", ok, err)
	}
	if l.Holder.CommitSHA != "bbb" {
		t.Fatalf("re-acquire should update commit sha")
	}
}

func TestAcquireSamePRDifferentRunRefused(t *testing.T) {
	// A double `/reeve apply` (or workflow re-run) on the same PR must not
	// run pulumi up concurrently: the second run is refused, not queued.
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	got, ok, err := TryAcquire(l, Holder{PR: 1, RunID: "run-b"}, time.Hour, t0.Add(time.Minute))
	if ok || !errors.Is(err, ErrHeldBySamePR) {
		t.Fatalf("expected ErrHeldBySamePR, got ok=%v err=%v", ok, err)
	}
	if got.Holder == nil || got.Holder.RunID != "run-a" {
		t.Fatalf("holder must stay run-a: %+v", got.Holder)
	}
	if len(got.Queue) != 0 {
		t.Fatalf("same PR must not queue behind itself: %+v", got.Queue)
	}
}

func TestAcquireSamePRDifferentRunTakesOverExpired(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	// run-a's lease expired: normal eviction/takeover applies.
	l, ok, err := TryAcquire(l, Holder{PR: 1, RunID: "run-b"}, time.Hour, t0.Add(2*time.Hour))
	if err != nil || !ok {
		t.Fatalf("expected takeover after expiry, got ok=%v err=%v", ok, err)
	}
	if l.Holder == nil || l.Holder.RunID != "run-b" {
		t.Fatalf("holder should be run-b: %+v", l.Holder)
	}
}

func TestReleasePromotesQueue(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-1"}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 3}, time.Hour, t0)
	l, err := Release(l, 1, "run-1", time.Hour, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.PR != 2 {
		t.Fatalf("expected pr 2 promoted, got %+v", l.Holder)
	}
	if len(l.Queue) != 1 || l.Queue[0].PR != 3 {
		t.Fatalf("queue wrong: %+v", l.Queue)
	}
}

func TestReleaseNotHolder(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	_, err := Release(l, 2, "", time.Hour, t0)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("expected ErrNotHolder, got %v", err)
	}
}

func TestReleaseFromQueueIsSilent(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	l, err := Release(l, 2, "", time.Hour, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("queued PR release should succeed silently: %v", err)
	}
	if len(l.Queue) != 0 {
		t.Fatalf("queue should be empty: %+v", l.Queue)
	}
}

func TestReleaseWrongRunIDDoesNotFree(t *testing.T) {
	// A stale or duplicate run of the same PR finishing must not free the
	// lock out from under the run that actually holds it.
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	got, err := Release(l, 1, "run-b", time.Hour, t0.Add(time.Minute))
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("expected ErrNotHolder, got %v", err)
	}
	if got.Holder == nil || got.Holder.RunID != "run-a" {
		t.Fatalf("holder must survive wrong-run release: %+v", got.Holder)
	}
}

func TestReleasePromotesWithConfiguredTTL(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-1"}, 30*time.Minute, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, 30*time.Minute, t0)
	rel := t0.Add(time.Minute)
	l, err := Release(l, 1, "run-1", 30*time.Minute, rel)
	if err != nil {
		t.Fatal(err)
	}
	want := rel.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	if l.Holder == nil || l.Holder.ExpiresAt != want {
		t.Fatalf("promoted holder must get the configured ttl: want expires=%s got %+v", want, l.Holder)
	}
}

func TestPromoteTTLFallsBackToDefault(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-1"}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	rel := t0.Add(time.Minute)
	l, err := Release(l, 1, "run-1", 0, rel)
	if err != nil {
		t.Fatal(err)
	}
	want := rel.Add(defaultPromoteTTL).UTC().Format(time.RFC3339)
	if l.Holder == nil || l.Holder.ExpiresAt != want {
		t.Fatalf("ttl<=0 must fall back to default: want expires=%s got %+v", want, l.Holder)
	}
}

func TestReapPromotesWithConfiguredTTL(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	later := t0.Add(2 * time.Hour)
	l, evicted := Reap(l, 30*time.Minute, later)
	if !evicted {
		t.Fatal("expected eviction")
	}
	want := later.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	if l.Holder == nil || l.Holder.ExpiresAt != want {
		t.Fatalf("reap-promoted holder must get the configured ttl: want expires=%s got %+v", want, l.Holder)
	}
}

func TestUnlockPRRunScopedKeepsOtherRunsHolder(t *testing.T) {
	// A finishing run leaving its PR everywhere must not evict a different
	// live run of the same PR that holds another stack's lock.
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	l = UnlockPR(l, 1, "run-b", time.Hour, t0.Add(time.Minute))
	if l.Holder == nil || l.Holder.RunID != "run-a" {
		t.Fatalf("run-scoped leave must not evict a different live run: %+v", l.Holder)
	}
	// runID "" (admin / PR-close cleanup) removes any run of the PR.
	l = UnlockPR(l, 1, "", time.Hour, t0.Add(2*time.Minute))
	if l.Holder != nil {
		t.Fatalf("unscoped leave should clear the holder: %+v", l.Holder)
	}
}

func TestExpiredHolderEvictedOnAcquire(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	// 2 hours later, PR 2 shows up
	later := t0.Add(2 * time.Hour)
	l, ok, err := TryAcquire(l, Holder{PR: 2}, time.Hour, later)
	if err != nil || !ok {
		t.Fatalf("expected pr 2 to acquire after eviction, got ok=%v err=%v", ok, err)
	}
	if l.Holder.PR != 2 {
		t.Fatalf("holder should be pr 2: %+v", l.Holder)
	}
}

func TestCorruptedExpiresAtTreatedAsExpired(t *testing.T) {
	// A lock blob with a malformed ExpiresAt would otherwise be immortal -
	// the eviction check returned false on parse failure. The reaper must
	// be able to clear it.
	corrupted := &Holder{PR: 1, ExpiresAt: "not-a-timestamp"}
	if !expired(corrupted, t0) {
		t.Fatal("expected corrupted ExpiresAt to be treated as expired")
	}
}

func TestReapEvictsAndPromotes(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	later := t0.Add(2 * time.Hour)
	l, evicted := Reap(l, time.Hour, later)
	if !evicted {
		t.Fatal("expected eviction")
	}
	if l.Holder == nil || l.Holder.PR != 2 {
		t.Fatalf("expected pr 2 promoted after reap: %+v", l.Holder)
	}
}

func TestUnlockPRRemovesAcrossHolderAndQueue(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 3}, time.Hour, t0)
	l = UnlockPR(l, 2, "", time.Hour, t0.Add(time.Minute)) // queued PR leaves
	if len(l.Queue) != 1 || l.Queue[0].PR != 3 {
		t.Fatalf("expected queue to be [3], got %+v", l.Queue)
	}
	l = UnlockPR(l, 1, "", time.Hour, t0.Add(2*time.Minute)) // holder leaves, promotes 3
	if l.Holder == nil || l.Holder.PR != 3 {
		t.Fatalf("expected pr 3 promoted: %+v", l.Holder)
	}
}

func TestStatus(t *testing.T) {
	l := NewLock("api", "prod", t0)
	if l.Status(t0) != StatusFree {
		t.Fatal("new lock should be free")
	}
	l, _, _ = TryAcquire(l, Holder{PR: 1}, time.Hour, t0)
	if l.Status(t0) != StatusHeld {
		t.Fatal("should be held")
	}
	if l.Status(t0.Add(2*time.Hour)) != StatusExpired {
		t.Fatal("should be expired past ttl")
	}
}

// TestTryAcquireTransitions is the full transition table for holder
// identity + provenance. "promoted" holders are dead reservations installed
// by promoteNext; "acquired" holders came from their own TryAcquire.
func TestTryAcquireTransitions(t *testing.T) {
	seedAcquired := func() Lock {
		l := NewLock("api", "prod", t0)
		l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
		return l
	}
	seedPromoted := func() Lock {
		// PR 1 queues behind PR 9; PR 9 releases → PR 1 promoted (dead).
		l := NewLock("api", "prod", t0)
		l, _, _ = TryAcquire(l, Holder{PR: 9, RunID: "other"}, time.Hour, t0)
		l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
		l, err := Release(l, 9, "other", time.Hour, t0.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if l.Holder == nil || !l.Holder.Promoted || l.Holder.PR != 1 {
			t.Fatalf("seed: expected promoted holder for PR 1: %+v", l.Holder)
		}
		return l
	}

	cases := []struct {
		name         string
		seed         func() Lock
		applicant    Holder
		at           time.Time
		wantAcquired bool
		wantErr      error
		wantRunID    string // expected holder RunID after the call
		wantPromoted bool
	}{
		{
			name: "same PR same run refreshes",
			seed: seedAcquired, applicant: Holder{PR: 1, RunID: "run-a"},
			at: t0.Add(10 * time.Minute), wantAcquired: true, wantErr: ErrAlreadyHolder,
			wantRunID: "run-a", wantPromoted: false,
		},
		{
			name: "same PR different run vs promoted holder takes over",
			seed: seedPromoted, applicant: Holder{PR: 1, RunID: "run-b"},
			at: t0.Add(10 * time.Minute), wantAcquired: true, wantErr: nil,
			wantRunID: "run-b", wantPromoted: false,
		},
		{
			name: "same PR different run vs active holder refused",
			seed: seedAcquired, applicant: Holder{PR: 1, RunID: "run-b"},
			at: t0.Add(10 * time.Minute), wantAcquired: false, wantErr: ErrHeldBySamePR,
			wantRunID: "run-a", wantPromoted: false,
		},
		{
			name: "same PR different run vs expired active holder takes over",
			seed: seedAcquired, applicant: Holder{PR: 1, RunID: "run-b"},
			at: t0.Add(2 * time.Hour), wantAcquired: true, wantErr: nil,
			wantRunID: "run-b", wantPromoted: false,
		},
		{
			name: "different PR vs active holder queues",
			seed: seedAcquired, applicant: Holder{PR: 2, RunID: "run-x"},
			at: t0.Add(10 * time.Minute), wantAcquired: false, wantErr: nil,
			wantRunID: "run-a", wantPromoted: false,
		},
		{
			name: "different PR vs promoted holder queues (reservation is real for other PRs)",
			seed: seedPromoted, applicant: Holder{PR: 2, RunID: "run-x"},
			at: t0.Add(10 * time.Minute), wantAcquired: false, wantErr: nil,
			wantRunID: "run-a", wantPromoted: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, ok, err := TryAcquire(tc.seed(), tc.applicant, time.Hour, tc.at)
			if ok != tc.wantAcquired || !errors.Is(err, tc.wantErr) {
				t.Fatalf("acquired=%v err=%v, want acquired=%v err=%v", ok, err, tc.wantAcquired, tc.wantErr)
			}
			if l.Holder == nil {
				t.Fatal("holder must never be nil after these transitions")
			}
			if l.Holder.RunID != tc.wantRunID {
				t.Fatalf("holder run = %q, want %q", l.Holder.RunID, tc.wantRunID)
			}
			if l.Holder.Promoted != tc.wantPromoted {
				t.Fatalf("holder promoted = %v, want %v", l.Holder.Promoted, tc.wantPromoted)
			}
		})
	}
}

func TestPromotedTakeoverGetsFreshLease(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 9, RunID: "other"}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	l, _ = Release(l, 9, "other", time.Hour, t0)
	at := t0.Add(30 * time.Minute)
	l, ok, err := TryAcquire(l, Holder{PR: 1, RunID: "run-b", CommitSHA: "ccc", Actor: "alice"}, time.Hour, at)
	if err != nil || !ok {
		t.Fatalf("takeover: ok=%v err=%v", ok, err)
	}
	wantExp := at.Add(time.Hour).UTC().Format(time.RFC3339)
	if l.Holder.ExpiresAt != wantExp {
		t.Fatalf("takeover lease = %s, want %s", l.Holder.ExpiresAt, wantExp)
	}
	if l.Holder.CommitSHA != "ccc" || l.Holder.Actor != "alice" {
		t.Fatalf("takeover must adopt the applicant identity: %+v", l.Holder)
	}
}

func TestReapedPromotionMarksPromoted(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 2, RunID: "run-b"}, time.Hour, t0)
	l, evicted := Reap(l, time.Hour, t0.Add(2*time.Hour))
	if !evicted || l.Holder == nil || l.Holder.PR != 2 {
		t.Fatalf("reap should promote PR 2: evicted=%v holder=%+v", evicted, l.Holder)
	}
	if !l.Holder.Promoted {
		t.Fatal("reaper-promoted holder must carry the Promoted marker")
	}
}

func TestHolderPromotedJSONRoundTrip(t *testing.T) {
	l := NewLock("api", "prod", t0)
	l, _, _ = TryAcquire(l, Holder{PR: 9, RunID: "other"}, time.Hour, t0)
	l, _, _ = TryAcquire(l, Holder{PR: 1, RunID: "run-a"}, time.Hour, t0)
	l, _ = Release(l, 9, "other", time.Hour, t0)

	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"promoted":true`) {
		t.Fatalf("promoted flag must serialize: %s", data)
	}
	var back Lock
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Holder == nil || !back.Holder.Promoted {
		t.Fatalf("promoted flag must round-trip: %+v", back.Holder)
	}
}

func TestOldLockBlobWithoutPromotedFieldLoads(t *testing.T) {
	// A pre-upgrade blob has no "promoted" key. It must decode with
	// Promoted=false (actively acquired), keeping ErrHeldBySamePR semantics.
	old := []byte(`{
		"project": "api",
		"stack": "prod",
		"holder": {
			"pr": 1, "commit_sha": "aaa", "run_id": "run-a", "actor": "alice",
			"acquired_at": "2026-04-20T12:00:00Z", "expires_at": "2026-04-20T16:00:00Z"
		},
		"queue": [],
		"updated_at": "2026-04-20T12:00:00Z"
	}`)
	var l Lock
	if err := json.Unmarshal(old, &l); err != nil {
		t.Fatal(err)
	}
	if l.Holder == nil || l.Holder.Promoted {
		t.Fatalf("legacy holder must load as actively acquired: %+v", l.Holder)
	}
	_, ok, err := TryAcquire(l, Holder{PR: 1, RunID: "run-b"}, time.Hour, t0.Add(time.Minute))
	if ok || !errors.Is(err, ErrHeldBySamePR) {
		t.Fatalf("legacy active holder must still refuse a second run: ok=%v err=%v", ok, err)
	}
}
