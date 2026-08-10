package blob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrConditionalWritesUnsupported means the backing bucket accepted a
// conditional create for a key that already existed. Every guarantee built
// on compare-and-swap - lock ownership, write-once audit entries - rests on
// the backend enforcing If-Match / If-None-Match. A backend that ignores
// them (older MinIO, historical R2) turns those guarantees into fiction, so
// callers refuse to operate rather than pretend.
var ErrConditionalWritesUnsupported = errors.New(
	"bucket does not enforce conditional writes (If-None-Match/If-Match); locks and write-once audit entries would be unsafe - use a backend with conditional-write support (real S3, current MinIO/R2, GCS, Azure Blob, or the filesystem store)")

// casProbePrefix is deliberately outside every meaningful prefix: probe
// objects must not appear in a listing of locks/, audit/ or runs/. They are
// deleted best-effort, and a leaked one is inert.
const casProbePrefix = ".cas-probe/"

// CASProbe verifies, once per instance, that a store actually enforces
// conditional writes. The zero value is ready to use and safe for
// concurrent use.
type CASProbe struct {
	mu     sync.Mutex
	probed bool
	err    error
}

// Ensure returns nil if the store enforces conditional writes.
//
// Only definitive verdicts are cached: "enforced" and
// ErrConditionalWritesUnsupported. A transient failure - a network blip, a
// cancelled context - leaves the probe re-runnable, so one bad moment
// cannot convince a process for the rest of its life that the bucket is
// unsafe.
func (p *CASProbe) Ensure(ctx context.Context, s Store) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.probed {
		return p.err
	}
	err := probeConditionalWrites(ctx, s)
	if err == nil || errors.Is(err, ErrConditionalWritesUnsupported) {
		p.probed = true
		p.err = err
	}
	return err
}

// probeConditionalWrites verifies BOTH halves of the compare-and-swap
// contract, because lock correctness needs both:
//
//  1. If-None-Match:* - a second create of an existing key must fail. This
//     is what makes "acquire an unheld lock" exclusive.
//  2. If-Match:<etag> - a write carrying a stale ETag must fail. This is
//     what makes the mutate loop's read-modify-write safe; a backend can
//     honour create-if-absent and still ignore ETags on update, in which
//     case two runs would happily overwrite each other's lock state.
//
// Some S3-compatibles accept the If-* headers and silently ignore them, so
// the probe asserts the failures rather than trusting the headers were
// understood. The probe object is deleted best-effort.
func probeConditionalWrites(ctx context.Context, s Store) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("conditional-write probe: %w", err)
	}
	key := casProbePrefix + hex.EncodeToString(suffix[:])
	defer func() {
		_ = s.Delete(ctx, key)
	}()

	// 1. Create-if-absent must succeed, then must fail on the second try.
	first, err := s.PutIfMatch(ctx, key, strings.NewReader("probe"), "")
	if err != nil {
		return fmt.Errorf("conditional-write probe: initial create failed: %w", err)
	}
	switch _, err := s.PutIfMatch(ctx, key, strings.NewReader("probe2"), ""); {
	case err == nil:
		return ErrConditionalWritesUnsupported
	case !errors.Is(err, ErrPreconditionFailed):
		return fmt.Errorf("conditional-write probe: %w", err)
	}

	// 2. If-Match. The ETag comes from the create when the backend returns
	// one, otherwise from a read - Store implementations are not required
	// to populate metadata on write.
	etag := ""
	if first != nil {
		etag = first.ETag
	}
	if etag == "" {
		rc, md, err := s.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("conditional-write probe: read back failed: %w", err)
		}
		_ = rc.Close()
		if md != nil {
			etag = md.ETag
		}
	}
	if etag == "" {
		// Without an ETag the caller's mutate loop has nothing to compare,
		// so the guarantee cannot hold whatever the backend does with the
		// header.
		return fmt.Errorf("%w: backend returns no ETag to compare against", ErrConditionalWritesUnsupported)
	}

	// A write carrying the current ETag must succeed...
	updated, err := s.PutIfMatch(ctx, key, strings.NewReader("probe3"), etag)
	if err != nil {
		return fmt.Errorf("conditional-write probe: matched update failed: %w", err)
	}
	// ...and that same ETag, now stale, must be refused.
	newETag := ""
	if updated != nil {
		newETag = updated.ETag
	}
	if newETag == etag {
		// The object changed but the ETag did not, so If-Match can never
		// detect a lost update.
		return fmt.Errorf("%w: ETag did not change after a write", ErrConditionalWritesUnsupported)
	}
	switch _, err := s.PutIfMatch(ctx, key, strings.NewReader("probe4"), etag); {
	case err == nil:
		return ErrConditionalWritesUnsupported
	case !errors.Is(err, ErrPreconditionFailed):
		return fmt.Errorf("conditional-write probe: %w", err)
	}
	return nil
}
