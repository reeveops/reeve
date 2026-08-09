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

// probeConditionalWrites creates a probe object with If-None-Match:*, then
// attempts a second conditional create of the same key, which MUST fail
// with a precondition error. Some S3-compatibles accept the If-* headers
// and silently ignore them - both writes succeed, and every guarantee built
// on top would be imaginary.
func probeConditionalWrites(ctx context.Context, s Store) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("conditional-write probe: %w", err)
	}
	key := casProbePrefix + hex.EncodeToString(suffix[:])
	defer func() {
		_ = s.Delete(ctx, key)
	}()
	if _, err := s.PutIfMatch(ctx, key, strings.NewReader("probe"), ""); err != nil {
		return fmt.Errorf("conditional-write probe: initial create failed: %w", err)
	}
	_, err := s.PutIfMatch(ctx, key, strings.NewReader("probe2"), "")
	switch {
	case err == nil:
		// A second create-if-absent of an existing key succeeded: the
		// backend is not enforcing conditions. Fail loudly.
		return ErrConditionalWritesUnsupported
	case errors.Is(err, ErrPreconditionFailed):
		return nil // enforced, as required
	default:
		return fmt.Errorf("conditional-write probe: %w", err)
	}
}
