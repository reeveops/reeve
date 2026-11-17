// Package locks is the pure lock state machine. No I/O - the storage
// interface (blob.Store with If-Match writes) is injected. See
// openspec/specs/core/locking.
package locks

import (
	"errors"
	"fmt"
	"time"
)

// Status describes the current state of a lock on disk.
type Status string

const (
	StatusFree    Status = "free"
	StatusHeld    Status = "held"
	StatusExpired Status = "expired"
)

// Lock is the serialized shape written to blob. One file per stack:
// locks/{project}/{stack}.json.
type Lock struct {
	Project   string      `json:"project"`
	Stack     string      `json:"stack"`
	Holder    *Holder     `json:"holder,omitempty"` // nil if free
	Queue     []QueueItem `json:"queue"`            // FIFO; holder not included
	UpdatedAt string      `json:"updated_at"`       // RFC3339
}

// Holder is who currently holds the lock.
type Holder struct {
	PR         int    `json:"pr"`
	CommitSHA  string `json:"commit_sha"`
	RunID      string `json:"run_id"`
	Actor      string `json:"actor"`
	AcquiredAt string `json:"acquired_at"` // RFC3339
	ExpiresAt  string `json:"expires_at"`  // RFC3339
	// Promoted marks provenance: true when this holder was installed from
	// the queue by promoteNext (release/unlock/reaper), not by its own
	// TryAcquire. Queued runs have already exited (blocked runs don't
	// wait), so a promoted holder is a dead reservation for its PR - the
	// next run of that PR may adopt it instead of being refused with
	// ErrHeldBySamePR. Cleared the moment a live run (re)acquires. Old
	// lock blobs without the field decode to false, which is correct:
	// their holders were actively acquired.
	Promoted bool `json:"promoted,omitempty"`
	// HoldStarted marks that the run's APPLY phase began for this holder:
	// the lease no longer bounds the hold and expiry never frees the lock
	// or lets another PR acquire - the hold ends only when the apply
	// releases it or an explicit unlock clears it. Old blobs decode to
	// false: pre-decision locks keep their lease semantics.
	HoldStarted bool `json:"hold_started,omitempty"`
	// Outcome records why the lock is STILL held after its run ended:
	// "failed" | "partial" | "interrupted". Empty while the run is live.
	// A failed or interrupted apply leaves the lock held with its reason -
	// released only by a successful apply or an explicit unlock - and
	// everyone meeting the lock sees this reason.
	Outcome string `json:"outcome,omitempty"`
	// RunURL points at the CI run that produced the recorded outcome; it
	// is the "where is the full detail" pointer every refusal renders
	// alongside the audit record key.
	RunURL string `json:"run_url,omitempty"`
	// AuditKey names the write-once audit record carrying the run's full
	// detail (its intent/completion entries).
	AuditKey string `json:"audit_key,omitempty"`
	// RecordedAt stamps when the outcome was recorded.
	RecordedAt string `json:"recorded_at,omitempty"`
}

// HoldStatus renders the observable hold facts a lock reader needs: whether
// the apply's persistent hold started, where the lease stood, and whether an
// outcome was recorded. The one ambiguous state is a holder that started its
// hold with NO recorded outcome and an expired lease: nothing was released
// and nothing failed cleanly, so the run most likely died (runner killed) -
// the reader must validate the stack's real state before unlocking. Every
// surface that shows a holder renders through this or its own wording of it.
func (h *Holder) HoldStatus(now time.Time) string {
	if h == nil {
		return ""
	}
	status := ""
	leaseExpired := false
	if h.HoldStarted {
		status = "hold started"
		if exp, err := time.Parse(time.RFC3339, h.ExpiresAt); err == nil {
			if exp.Before(now) {
				leaseExpired = true
				status += ", lease expired at " + h.ExpiresAt
			} else {
				status += fmt.Sprintf(", lease expires %s", h.ExpiresAt)
			}
		}
	}
	switch {
	case h.Outcome != "":
		status += fmt.Sprintf(", recorded outcome %q", h.Outcome)
		if h.RecordedAt != "" {
			status += fmt.Sprintf(" (recorded %s)", h.RecordedAt)
		}
		if h.RunURL != "" {
			status += "; run " + h.RunURL
		}
		if h.AuditKey != "" {
			status += "; audit record `" + h.AuditKey + "`"
		}
	case h.HoldStarted && leaseExpired:
		// Started, lease gone, nothing recorded: nothing was released and
		// nothing failed cleanly, so the runner most likely died.
		status += ", no outcome recorded - the run most likely died (runner killed); validate the stack's real state before unlocking"
	case h.HoldStarted:
		// Started and still inside its lease: the apply is (most likely)
		// in progress. No guess about death while the lease lives.
		status += ", no outcome recorded - the apply is in progress"
	}
	return status
}

// QueueItem is a waiting applicant.
type QueueItem struct {
	PR         int    `json:"pr"`
	CommitSHA  string `json:"commit_sha"`
	RunID      string `json:"run_id"`
	Actor      string `json:"actor"`
	EnqueuedAt string `json:"enqueued_at"`
}

// ErrAlreadyHolder signals the caller is already the holder (idempotent
// re-acquire; callers typically treat as success).
var ErrAlreadyHolder = errors.New("already holding lock")

// ErrAlreadyQueued signals the caller is already waiting in the queue.
var ErrAlreadyQueued = errors.New("already queued")

// ErrHeldBySamePR signals a DIFFERENT run of the same PR currently holds
// the lock. The applicant must not apply concurrently and is not queued
// (a PR never waits behind itself); it should surface "another run of
// this PR holds the lock" and back off until the holder finishes or its
// lease expires.
var ErrHeldBySamePR = errors.New("lock held by another run of the same PR")

// ErrNotHolder is returned by Release when the caller does not currently
// hold the lock.
var ErrNotHolder = errors.New("not the current holder")

// LeaseLostError reports that a refresh-only lease extension found the
// caller no longer holding the lock. The fields state what the lock looked
// like at the failed refresh, so the caller can say what remains instead of
// a bare "lost" - another PR holds it, another run of the same PR holds it,
// or nobody does (released or reaped).
type LeaseLostError struct {
	// Released is true when the lock had no holder at all.
	Released bool
	// HeldByPR is the PR recorded as holder when the loss was seen. 0 when
	// the lock was free.
	HeldByPR int
	// HeldByOtherRun reports the caller's own PR holding the lock through a
	// different run ID.
	HeldByOtherRun bool
}

func (e *LeaseLostError) Error() string {
	switch {
	case e.Released:
		return "lease lost: the lock has no holder"
	case e.HeldByPR != 0 && e.HeldByOtherRun:
		return fmt.Sprintf("lease lost: now held by another run of PR #%d", e.HeldByPR)
	case e.HeldByPR != 0:
		return fmt.Sprintf("lease lost: now held by PR #%d", e.HeldByPR)
	default:
		return "lease lost: lock state did not match the caller"
	}
}

// NewLock returns a fresh free lock.
func NewLock(project, stack string, now time.Time) Lock {
	return Lock{
		Project:   project,
		Stack:     stack,
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
}

// TryAcquire transitions the lock. Returns the new state plus either
// - acquired=true: caller is now the holder
// - acquired=false: caller is queued (or already queued)
//
// Expired holders are evicted. Holder identity is PR+RunID:
//   - same PR + same RunID: idempotent refresh of expires_at.
//   - same PR + different RunID + holder PROMOTED from the queue: the
//     applicant adopts the reservation (queued runs already exited, so a
//     promoted holder can never be a live process) - new RunID, fresh
//     lease, Promoted cleared.
//   - same PR + different RunID + holder actively acquired: refused with
//     ErrHeldBySamePR while the lease is unexpired (two live runs of one
//     PR must never apply concurrently).
func TryAcquire(l Lock, applicant Holder, ttl time.Duration, now time.Time) (Lock, bool, error) {
	l = evictIfExpired(l, now, ttl)

	// Same PR already holds.
	if l.Holder != nil && l.Holder.PR == applicant.PR {
		if l.Holder.RunID != applicant.RunID {
			if !l.Holder.Promoted {
				// A started hold is never expiry-freeable, so this refusal
				// lasts until the run releases or an explicit unlock -
				// the message the caller renders says so with the holder's
				// recorded reason.
				return l, false, ErrHeldBySamePR
			}
			// Promoted dead reservation: adopt it as a fresh acquire.
			return install(l, applicant, ttl, now), true, nil
		}
		refreshed := *l.Holder
		refreshed.ExpiresAt = now.Add(ttl).UTC().Format(time.RFC3339)
		refreshed.CommitSHA = applicant.CommitSHA
		refreshed.RunID = applicant.RunID
		refreshed.Actor = applicant.Actor
		// A live run is refreshing: whatever the provenance was, the holder
		// is actively held now.
		refreshed.Promoted = false
		l.Holder = &refreshed
		l.UpdatedAt = now.UTC().Format(time.RFC3339)
		return l, true, ErrAlreadyHolder
	}

	if l.Holder == nil {
		return install(l, applicant, ttl, now), true, nil
	}

	// Already held by someone else - enqueue.
	for _, q := range l.Queue {
		if q.PR == applicant.PR {
			return l, false, ErrAlreadyQueued
		}
	}
	l.Queue = append(l.Queue, QueueItem{
		PR:         applicant.PR,
		CommitSHA:  applicant.CommitSHA,
		RunID:      applicant.RunID,
		Actor:      applicant.Actor,
		EnqueuedAt: now.UTC().Format(time.RFC3339),
	})
	l.UpdatedAt = now.UTC().Format(time.RFC3339)
	return l, false, nil
}

// RefreshLease extends the holder's lease when the lock's CURRENT holder
// still identifies the caller (same PR and run). It is refresh-only: unlike
// TryAcquire it never evicts a foreign holder, never installs a holder,
// never adopts a promoted reservation, and never enqueues. Any other state
// is a *LeaseLostError with the observed lock state, and the lock is
// returned unchanged.
//
// A lease whose own expiry has already passed still refreshes when the
// stored holder is the caller: the CAS write proves the caller is writing
// over live state it still owns - if another party had taken over, the
// conditional write fails and the retry re-reads them as the holder (the
// same interleaving the acquire path handles). What must never happen is
// the old heartbeat behavior: reusing TryAcquire, which reinstalled a
// holder after a force-unlock cleared it and let an ownerless apply run on.
func RefreshLease(l Lock, pr int, runID string, ttl time.Duration, now time.Time) (Lock, error) {
	h := l.Holder
	if h == nil {
		return l, &LeaseLostError{Released: true}
	}
	if h.PR != pr || h.RunID != runID {
		return l, &LeaseLostError{HeldByPR: h.PR, HeldByOtherRun: h.PR == pr}
	}
	refreshed := *h
	refreshed.ExpiresAt = now.Add(promoteTTL(ttl)).UTC().Format(time.RFC3339)
	// A live run is refreshing: whatever the provenance was, the holder is
	// actively held now.
	refreshed.Promoted = false
	l.Holder = &refreshed
	l.UpdatedAt = now.UTC().Format(time.RFC3339)
	return l, nil
}

// Release transfers the lock to the next queued applicant (or frees it).
// Only the current holder - matched by pr AND runID - can release. A
// non-holder release falls back to queue-removal semantics. ttl bounds the
// promoted holder's lease; <=0 falls back to defaultPromoteTTL.
func Release(l Lock, pr int, runID string, ttl time.Duration, now time.Time) (Lock, error) {
	l = evictIfExpired(l, now, ttl)
	if l.Holder == nil || l.Holder.PR != pr || l.Holder.RunID != runID {
		// Removing from queue is always safe.
		before := len(l.Queue)
		l.Queue = removePR(l.Queue, pr)
		if len(l.Queue) != before {
			l.UpdatedAt = now.UTC().Format(time.RFC3339)
			return l, nil
		}
		return l, ErrNotHolder
	}
	l.Holder = nil
	l = promoteNext(l, now, promoteTTL(ttl))
	l.UpdatedAt = now.UTC().Format(time.RFC3339)
	return l, nil
}

// UnlockPR removes pr from holder and queue without erroring if absent.
// Used for PR closed / merged cleanup across all stacks. runID scopes the
// holder removal: "" matches any run of the pr (admin/PR-close cleanup);
// a non-empty runID only clears a holder from that same run (or one whose
// lease has expired), so a finishing run cannot evict a different live run
// of its own PR. Queue entries for pr are removed unconditionally. ttl
// bounds the promoted holder's lease; <=0 falls back to defaultPromoteTTL.
func UnlockPR(l Lock, pr int, runID string, ttl time.Duration, now time.Time) Lock {
	if l.Holder != nil && l.Holder.PR == pr &&
		(runID == "" || l.Holder.RunID == runID || expired(l.Holder, now)) {
		l.Holder = nil
		l = promoteNext(l, now, promoteTTL(ttl))
	}
	l.Queue = removePR(l.Queue, pr)
	l.UpdatedAt = now.UTC().Format(time.RFC3339)
	return l
}

// Reap evicts an expired holder if present. Returns (lock, evicted). ttl
// bounds the promoted holder's lease; <=0 falls back to defaultPromoteTTL.
// A holder whose apply started (HoldStarted) is never evicted: the hold
// ends only with the apply's own release or an explicit unlock.
func Reap(l Lock, ttl time.Duration, now time.Time) (Lock, bool) {
	if l.Holder == nil || l.Holder.HoldStarted {
		return l, false
	}
	if expired(l.Holder, now) {
		l.Holder = nil
		l = promoteNext(l, now, promoteTTL(ttl))
		l.UpdatedAt = now.UTC().Format(time.RFC3339)
		return l, true
	}
	return l, false
}

// Status returns the current status as seen at now.
func (l Lock) Status(now time.Time) Status {
	if l.Holder == nil {
		return StatusFree
	}
	if expired(l.Holder, now) {
		return StatusExpired
	}
	return StatusHeld
}

// --- helpers ---

// defaultPromoteTTL is the promoted-holder lease fallback for callers that
// do not thread the configured locking.ttl (ttl <= 0).
const defaultPromoteTTL = 4 * time.Hour

// promoteTTL resolves the lease duration for a holder promoted from the
// queue: the configured ttl when set, defaultPromoteTTL otherwise.
func promoteTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultPromoteTTL
	}
	return ttl
}

// install makes applicant the holder with a fresh lease. Direct acquire
// provenance: Promoted is always cleared.
func install(l Lock, applicant Holder, ttl time.Duration, now time.Time) Lock {
	applicant.AcquiredAt = now.UTC().Format(time.RFC3339)
	applicant.ExpiresAt = now.Add(ttl).UTC().Format(time.RFC3339)
	applicant.Promoted = false
	l.Holder = &applicant
	// If applicant was also queued (race), remove from queue.
	l.Queue = removePR(l.Queue, applicant.PR)
	l.UpdatedAt = now.UTC().Format(time.RFC3339)
	return l
}

// evictIfExpired clears an expired lease holder - EXCEPT one whose apply
// has started (HoldStarted): a held lock never lapses on its own, whatever
// the heartbeat does or how the lease aged. It is released only by the
// apply's own successful completion or an explicit unlock.
func evictIfExpired(l Lock, now time.Time, ttl time.Duration) Lock {
	if l.Holder != nil && expired(l.Holder, now) && !l.Holder.HoldStarted {
		l.Holder = nil
		l = promoteNext(l, now, promoteTTL(ttl))
	}
	return l
}

func promoteNext(l Lock, now time.Time, ttl time.Duration) Lock {
	if l.Holder != nil || len(l.Queue) == 0 {
		return l
	}
	next := l.Queue[0]
	l.Queue = l.Queue[1:]
	l.Holder = &Holder{
		PR:         next.PR,
		CommitSHA:  next.CommitSHA,
		RunID:      next.RunID,
		Actor:      next.Actor,
		AcquiredAt: now.UTC().Format(time.RFC3339),
		ExpiresAt:  now.Add(ttl).UTC().Format(time.RFC3339),
		// The queued run already exited (blocked runs don't wait): mark the
		// reservation so the next run of this PR can adopt it instead of
		// being locked out by ErrHeldBySamePR for a full ttl.
		Promoted: true,
	}
	return l
}

// expired reports whether a lock holder's lease has elapsed. A nil holder or
// missing ExpiresAt is treated as not-expired (the caller hasn't promoted to
// owner yet). An unparseable ExpiresAt is treated as expired so a corrupted
// lock blob is evictable by the reaper instead of being immortal.
func expired(h *Holder, now time.Time) bool {
	if h == nil || h.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, h.ExpiresAt)
	if err != nil {
		return true
	}
	return now.After(exp)
}

// removePR returns q without the given PR's entries.
//
// The result is a fresh slice. Filtering in place through q[:0] writes into
// the caller's backing array, so a Lock value still held by the caller
// would see its Queue rewritten underneath it - Lock is passed by value,
// but the Queue header shares storage. It is latent today only because
// locks are decoded fresh from JSON before each transition. The approvals
// package hit the same aliasing bug for real (see core/approvals.Evaluate),
// and this package - whose whole point is pure value-semantics transitions
// - is the last place that should mutate its input.
func removePR(q []QueueItem, pr int) []QueueItem {
	if len(q) == 0 {
		return q
	}
	out := make([]QueueItem, 0, len(q))
	for _, item := range q {
		if item.PR != pr {
			out = append(out, item)
		}
	}
	return out
}
