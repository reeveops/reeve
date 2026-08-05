package locks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
)

// unconditionalStore simulates an S3-compatible backend that accepts
// If-Match / If-None-Match headers but silently ignores them: every
// conditional write succeeds. Locks on such a backend are fiction.
type unconditionalStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newUnconditionalStore() *unconditionalStore {
	return &unconditionalStore{m: map[string][]byte{}}
}

func (s *unconditionalStore) Get(_ context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return nil, nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), &blob.Metadata{ETag: "etag", Size: int64(len(b))}, nil
}

func (s *unconditionalStore) Put(_ context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = b
	return &blob.Metadata{ETag: "etag", Size: int64(len(b))}, nil
}

// PutIfMatch ignores the condition entirely - the bug under test.
func (s *unconditionalStore) PutIfMatch(ctx context.Context, key string, r io.Reader, _ string) (*blob.Metadata, error) {
	return s.Put(ctx, key, r)
}

func (s *unconditionalStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *unconditionalStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func TestCASProbeRejectsUnconditionalBackend(t *testing.T) {
	ctx := context.Background()
	s := New(newUnconditionalStore())
	s.Now = func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) }

	_, _, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 1, RunID: "r1"}, time.Hour)
	if !errors.Is(err, ErrConditionalWritesUnsupported) {
		t.Fatalf("expected ErrConditionalWritesUnsupported, got %v", err)
	}
	// Every mutating entry point must refuse, not just TryAcquire.
	if _, err := s.Release(ctx, "api", "prod", 1, "r1", time.Hour); !errors.Is(err, ErrConditionalWritesUnsupported) {
		t.Fatalf("Release: expected ErrConditionalWritesUnsupported, got %v", err)
	}
	if _, err := s.ForceUnlock(ctx, "api", "prod", time.Hour); !errors.Is(err, ErrConditionalWritesUnsupported) {
		t.Fatalf("ForceUnlock: expected ErrConditionalWritesUnsupported, got %v", err)
	}
}

// conditionCountingStore wraps a compliant store and counts conditional
// creates against probe keys, to assert the probe runs exactly once.
type conditionCountingStore struct {
	blob.Store
	mu         sync.Mutex
	probeIfNM  int // If-None-Match creates against probe keys
	probeAlive map[string]bool
}

func (c *conditionCountingStore) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	if strings.Contains(key, ".cas-probe/") && ifMatch == "" {
		c.mu.Lock()
		c.probeIfNM++
		if c.probeAlive == nil {
			c.probeAlive = map[string]bool{}
		}
		c.probeAlive[key] = true
		c.mu.Unlock()
	}
	return c.Store.PutIfMatch(ctx, key, r, ifMatch)
}

func (c *conditionCountingStore) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	delete(c.probeAlive, key)
	c.mu.Unlock()
	return c.Store.Delete(ctx, key)
}

func TestCASProbePassesOnceAndCleansUpOnCompliantBackend(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	counting := &conditionCountingStore{Store: fs}
	s := New(counting)
	s.Now = func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) }

	if _, ok, err := s.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 1, RunID: "r1"}, time.Hour); err != nil || !ok {
		t.Fatalf("acquire on compliant backend: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.TryAcquire(ctx, "worker", "prod", corelocks.Holder{PR: 2, RunID: "r2"}, time.Hour); err != nil || !ok {
		t.Fatalf("second acquire: ok=%v err=%v", ok, err)
	}

	counting.mu.Lock()
	defer counting.mu.Unlock()
	// Exactly the probe's two conditional creates (initial + must-fail),
	// regardless of how many mutations followed.
	if counting.probeIfNM != 2 {
		t.Fatalf("probe conditional creates = %d, want 2 (probe must run once per process)", counting.probeIfNM)
	}
	if len(counting.probeAlive) != 0 {
		t.Fatalf("probe objects leaked: %v", counting.probeAlive)
	}
}

func TestCASProbeObjectInvisibleToLockWalkers(t *testing.T) {
	// Even if cleanup failed, a leaked probe object must not confuse
	// ReapAll/ListAll (they skip non-.json keys).
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(ctx, "locks/.cas-probe/deadbeef", bytes.NewReader([]byte("probe"))); err != nil {
		t.Fatal(err)
	}
	s := New(fs)
	s.Now = func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) }
	got, err := s.ListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("leaked probe surfaced as a lock: %+v", got)
	}
}
