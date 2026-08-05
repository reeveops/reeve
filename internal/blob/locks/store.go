// Package locks is the blob-backed lock storage layer. Composes the pure
// lock state machine (internal/core/locks) with any blob.Store.
// Conditional writes via If-Match; on precondition failure the retry loop
// re-reads and reapplies.
package locks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
)

// Store wraps a blob.Store with lock-specific key conventions.
type Store struct {
	store blob.Store
	// MaxRetries bounds the optimistic-concurrency retry loop.
	MaxRetries int
	// Now is injectable for tests.
	Now func() time.Time

	// casOnce guards the one-time conditional-write probe (see ensureCAS).
	casOnce sync.Once
	casErr  error
}

// New returns a Store. MaxRetries defaults to 5.
func New(s blob.Store) *Store {
	return &Store{store: s, MaxRetries: 5, Now: time.Now}
}

// ErrConditionalWritesUnsupported means the backing bucket accepted a
// conditional create for a key that already existed. Every lock guarantee
// rests on the backend enforcing If-Match / If-None-Match; a backend that
// ignores them (older MinIO, historical R2) turns locks into silent
// no-ops, so we refuse to operate rather than pretend to lock.
var ErrConditionalWritesUnsupported = errors.New(
	"bucket does not enforce conditional writes (If-None-Match/If-Match); locks would be unsafe - use a backend with conditional-write support (real S3, current MinIO/R2, GCS, Azure Blob, or the filesystem store)")

// ensureCAS probes, once per Store, that the backend actually enforces
// conditional writes: create a probe object with If-None-Match:*, then
// attempt a second conditional create of the same key, which MUST fail
// with a precondition error. Some S3-compatibles accept the If-* headers
// and silently ignore them - both writes succeed and every "lock" this
// store hands out would be fiction. The probe object is deleted
// best-effort; the verdict is cached for the life of the process.
func (s *Store) ensureCAS(ctx context.Context) error {
	s.casOnce.Do(func() {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			s.casErr = fmt.Errorf("conditional-write probe: %w", err)
			return
		}
		key := "locks/.cas-probe/" + hex.EncodeToString(suffix[:])
		defer func() {
			// Best-effort cleanup; a leaked probe object is harmless (the
			// random suffix keeps it out of every lock key's way and
			// parseLockKey ignores non-.json keys).
			_ = s.store.Delete(ctx, key)
		}()
		if _, err := s.store.PutIfMatch(ctx, key, strings.NewReader("probe"), ""); err != nil {
			s.casErr = fmt.Errorf("conditional-write probe: initial create failed: %w", err)
			return
		}
		_, err := s.store.PutIfMatch(ctx, key, strings.NewReader("probe2"), "")
		switch {
		case err == nil:
			// Second create-if-absent of an existing key succeeded: the
			// backend is not enforcing conditions. Fail loudly.
			s.casErr = ErrConditionalWritesUnsupported
		case errors.Is(err, blob.ErrPreconditionFailed):
			s.casErr = nil // enforced, as required
		default:
			s.casErr = fmt.Errorf("conditional-write probe: %w", err)
		}
	})
	return s.casErr
}

func (s *Store) key(project, stack string) string {
	return fmt.Sprintf("locks/%s/%s.json", project, stack)
}

// Get reads the current lock state for a stack. Returns a fresh free lock
// if none exists yet.
func (s *Store) Get(ctx context.Context, project, stack string) (corelocks.Lock, string, error) {
	key := s.key(project, stack)
	rc, md, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return corelocks.NewLock(project, stack, s.Now()), "", nil
		}
		return corelocks.Lock{}, "", err
	}
	defer rc.Close()
	var l corelocks.Lock
	if err := json.NewDecoder(rc).Decode(&l); err != nil {
		return corelocks.Lock{}, "", fmt.Errorf("decode lock: %w", err)
	}
	return l, md.ETag, nil
}

// TryAcquire runs the acquire transition with optimistic concurrency.
// Returns acquired=true if the caller holds the lock after the call.
// acquired=false means the caller is queued - or, when the error is
// corelocks.ErrHeldBySamePR, that a different run of the same PR holds
// the lock and the caller must back off. The updated lock is always
// returned.
func (s *Store) TryAcquire(ctx context.Context, project, stack string, applicant corelocks.Holder, ttl time.Duration) (corelocks.Lock, bool, error) {
	return s.mutate(ctx, project, stack, func(cur corelocks.Lock) (corelocks.Lock, bool, error) {
		next, ok, err := corelocks.TryAcquire(cur, applicant, ttl, s.Now())
		if errors.Is(err, corelocks.ErrAlreadyHolder) {
			return next, true, nil // idempotent success
		}
		return next, ok, err
	})
}

// Release releases the lock. If pr+runID is the holder, the next queued
// applicant is promoted with a lease of ttl (<=0 falls back to the 4h
// default). A non-holder release only removes pr from the queue.
func (s *Store) Release(ctx context.Context, project, stack string, pr int, runID string, ttl time.Duration) (corelocks.Lock, error) {
	l, _, err := s.mutate(ctx, project, stack, func(cur corelocks.Lock) (corelocks.Lock, bool, error) {
		next, err := corelocks.Release(cur, pr, runID, ttl, s.Now())
		return next, false, err
	})
	return l, err
}

// StartHeartbeat spawns a goroutine that refreshes holder's lease every
// ttl/3 for as long as the work (an engine apply) runs, so an apply longer
// than locking.ttl is never evicted by the reaper mid-flight. The refresh
// reuses TryAcquire: same PR + same RunID is an idempotent lease extension.
//
// The heartbeat stops when ctx is cancelled or the returned stop func is
// called (stop blocks until the goroutine has exited; it is safe to call
// more than once). A refresh failure logs a warning and keeps trying -
// repeated failures mean the CAS store is unhealthy, which must be loud but
// must not kill a running apply.
func (s *Store) StartHeartbeat(ctx context.Context, project, stack string, holder corelocks.Holder, ttl time.Duration) (stop func()) {
	if s == nil || ttl <= 0 {
		return func() {}
	}
	interval := ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(exited)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_, ok, err := s.TryAcquire(ctx, project, stack, holder, ttl)
				switch {
				case err != nil && ctx.Err() == nil:
					slog.Warn("lock heartbeat refresh failed - CAS store may be unhealthy",
						"project", project, "stack", stack, "pr", holder.PR, "run_id", holder.RunID, "err", err)
				case err == nil && !ok:
					slog.Warn("lock heartbeat: no longer the holder - another party owns this lock",
						"project", project, "stack", stack, "pr", holder.PR, "run_id", holder.RunID)
				}
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		<-exited
	}
}

// ErrHolderActive is returned (force=false) when the PR holds the lock
// with an unexpired lease - most likely an apply still running. Callers
// surface a "re-run with --force" hint instead of clearing the holder.
var ErrHolderActive = errors.New("pr holds this lock with an active lease")

// holderActive reports whether pr (optionally scoped to runID) is the
// current holder with an unexpired lease.
func holderActive(l corelocks.Lock, pr int, runID string, now time.Time) bool {
	if l.Holder == nil || l.Holder.PR != pr {
		return false
	}
	if runID != "" && l.Holder.RunID != runID {
		return false
	}
	exp, err := time.Parse(time.RFC3339, l.Holder.ExpiresAt)
	return err == nil && now.Before(exp)
}

// UnlockPR removes pr from holder or queue across a stack. Silent if absent.
// Intended for PR merge/close cleanup. runID "" matches any run of the pr;
// a non-empty runID skips a holder from a different live run of the same
// pr untouched. ttl bounds the promoted holder's lease. force=false refuses
// (ErrHolderActive) to clear a holder whose lease is still active.
func (s *Store) UnlockPR(ctx context.Context, project, stack string, pr int, runID string, ttl time.Duration, force bool) (corelocks.Lock, error) {
	l, _, err := s.mutate(ctx, project, stack, func(cur corelocks.Lock) (corelocks.Lock, bool, error) {
		if !force && holderActive(cur, pr, runID, s.Now()) {
			return cur, false, ErrHolderActive
		}
		return corelocks.UnlockPR(cur, pr, runID, ttl, s.Now()), false, nil
	})
	return l, err
}

// UnlockPRAll removes pr from holder/queue across every stored lock.
// Returns the number of locks the pr was removed from plus the
// "project/stack" refs that were SKIPPED because the pr holds them with
// an active lease (force=false only; queue entries are always removed).
// Called by a finishing apply run (runID-scoped, force=true) so the PR
// does not linger in queues for stacks it no longer needs, and by
// `reeve locks unlock --pr N` / the "/reeve unlock" PR comment (runID "")
// for closed or abandoned PRs.
func (s *Store) UnlockPRAll(ctx context.Context, pr int, runID string, ttl time.Duration, force bool) (int, []string, error) {
	keys, err := s.store.List(ctx, "locks")
	if err != nil {
		return 0, nil, err
	}
	var n int
	var active []string
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		proj, stack, ok := parseLockKey(k)
		if !ok {
			continue
		}
		cur, _, err := s.Get(ctx, proj, stack)
		if err != nil {
			return n, active, err
		}
		if !involvesPR(cur, pr) {
			continue // avoid rewriting lock blobs the PR never touched
		}
		if _, err := s.UnlockPR(ctx, proj, stack, pr, runID, ttl, force); err != nil {
			if errors.Is(err, ErrHolderActive) {
				active = append(active, proj+"/"+stack)
				continue
			}
			return n, active, err
		}
		n++
	}
	return n, active, nil
}

func involvesPR(l corelocks.Lock, pr int) bool {
	if l.Holder != nil && l.Holder.PR == pr {
		return true
	}
	for _, q := range l.Queue {
		if q.PR == pr {
			return true
		}
	}
	return false
}

// ForceUnlock is the admin-override release. It clears the holder
// regardless of PR, promotes the queue with a lease of ttl (<=0 falls
// back to the 4h default). Callers verify admin auth before invoking
// (via shared.yaml locking.admin_override.allowed).
func (s *Store) ForceUnlock(ctx context.Context, project, stack string, ttl time.Duration) (corelocks.Lock, error) {
	l, _, err := s.mutate(ctx, project, stack, func(cur corelocks.Lock) (corelocks.Lock, bool, error) {
		cur.Holder = nil
		cur = forcePromoteQueue(cur, s.Now(), ttl)
		cur.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
		return cur, false, nil
	})
	return l, err
}

func forcePromoteQueue(l corelocks.Lock, now time.Time, ttl time.Duration) corelocks.Lock {
	if ttl <= 0 {
		ttl = 4 * time.Hour
	}
	if l.Holder != nil || len(l.Queue) == 0 {
		return l
	}
	next := l.Queue[0]
	l.Queue = l.Queue[1:]
	l.Holder = &corelocks.Holder{
		PR:         next.PR,
		CommitSHA:  next.CommitSHA,
		RunID:      next.RunID,
		Actor:      next.Actor,
		AcquiredAt: now.UTC().Format(time.RFC3339),
		ExpiresAt:  now.Add(ttl).UTC().Format(time.RFC3339),
		// Same provenance rule as corelocks.promoteNext: the queued run has
		// already exited, so the reservation is adoptable by its PR.
		Promoted: true,
	}
	return l
}

// Reap evicts an expired holder. Returns (lock, evicted). ttl bounds the
// promoted holder's lease; <=0 falls back to the 4h default.
func (s *Store) Reap(ctx context.Context, project, stack string, ttl time.Duration) (corelocks.Lock, bool, error) {
	var evicted bool
	l, _, err := s.mutate(ctx, project, stack, func(cur corelocks.Lock) (corelocks.Lock, bool, error) {
		next, ev := corelocks.Reap(cur, ttl, s.Now())
		evicted = ev
		return next, false, nil
	})
	return l, evicted, err
}

// ReapAll walks locks/ and reaps expired holders across every stack.
// Called opportunistically by reeve invocations and by `reeve locks reap`.
// ttl bounds promoted holders' leases; <=0 falls back to the 4h default.
func (s *Store) ReapAll(ctx context.Context, ttl time.Duration) (int, error) {
	keys, err := s.store.List(ctx, "locks")
	if err != nil {
		return 0, err
	}
	var n int
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		proj, stack, ok := parseLockKey(k)
		if !ok {
			continue
		}
		_, evicted, err := s.Reap(ctx, proj, stack, ttl)
		if err != nil {
			return n, err
		}
		if evicted {
			n++
		}
	}
	return n, nil
}

// ListAll returns every lock currently stored.
func (s *Store) ListAll(ctx context.Context) ([]corelocks.Lock, error) {
	keys, err := s.store.List(ctx, "locks")
	if err != nil {
		return nil, err
	}
	var out []corelocks.Lock
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		proj, stack, ok := parseLockKey(k)
		if !ok {
			continue
		}
		l, _, err := s.Get(ctx, proj, stack)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// mutate is the conditional-write retry loop.
func (s *Store) mutate(
	ctx context.Context, project, stack string,
	fn func(corelocks.Lock) (corelocks.Lock, bool, error),
) (corelocks.Lock, bool, error) {
	// Every lock mutation rides PutIfMatch; refuse to mutate at all on a
	// backend that does not enforce the precondition (probe runs once).
	if err := s.ensureCAS(ctx); err != nil {
		return corelocks.Lock{}, false, err
	}
	key := s.key(project, stack)
	for attempt := 0; attempt <= s.MaxRetries; attempt++ {
		cur, etag, err := s.Get(ctx, project, stack)
		if err != nil {
			return corelocks.Lock{}, false, err
		}
		next, flag, fnErr := fn(cur)
		data, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return corelocks.Lock{}, false, err
		}
		_, putErr := s.store.PutIfMatch(ctx, key, bytes.NewReader(data), etag)
		if putErr == nil {
			return next, flag, fnErr
		}
		if !errors.Is(putErr, blob.ErrPreconditionFailed) {
			return corelocks.Lock{}, false, putErr
		}
		// Lost the race - retry.
	}
	return corelocks.Lock{}, false, fmt.Errorf("lock %s/%s: exceeded %d retries", project, stack, s.MaxRetries)
}

// parseLockKey decodes "locks/<project>/<stack>.json".
func parseLockKey(k string) (string, string, bool) {
	k = strings.TrimPrefix(k, "locks/")
	k = strings.TrimSuffix(k, ".json")
	parts := strings.SplitN(k, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
