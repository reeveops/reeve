package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// halfEnforcingStore honours If-None-Match (create-if-absent) but ignores
// If-Match entirely: a write carrying a stale ETag still succeeds. That is
// the shape the probe used to miss, and it is the one that breaks the lock
// store's read-modify-write loop rather than its acquire.
type halfEnforcingStore struct {
	mu      sync.Mutex
	m       map[string][]byte
	version int
}

func (s *halfEnforcingStore) Get(_ context.Context, key string) (io.ReadCloser, *Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return nil, nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), &Metadata{ETag: s.etagLocked()}, nil
}

func (s *halfEnforcingStore) etagLocked() string {
	return string(rune('a' + s.version%26))
}

func (s *halfEnforcingStore) put(key string, r io.Reader) (*Metadata, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[key] = b
	s.version++
	return &Metadata{ETag: s.etagLocked()}, nil
}

func (s *halfEnforcingStore) Put(_ context.Context, key string, r io.Reader) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(key, r)
}

func (s *halfEnforcingStore) PutIfMatch(_ context.Context, key string, r io.Reader, ifMatch string) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ifMatch == "" {
		if _, exists := s.m[key]; exists {
			return nil, ErrPreconditionFailed // create-if-absent IS enforced
		}
	}
	// If-Match is ignored: any ETag, stale or not, is accepted.
	return s.put(key, r)
}

func (s *halfEnforcingStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *halfEnforcingStore) List(_ context.Context, _ string) ([]string, error) { return nil, nil }

// TestProbeRejectsBackendThatIgnoresIfMatch is the gap the first version of
// this probe had: it only exercised create-if-absent, so a backend like
// this one passed while every read-modify-write the lock store performs
// could silently lose an update.
func TestProbeRejectsBackendThatIgnoresIfMatch(t *testing.T) {
	t.Parallel()
	var p CASProbe
	err := p.Ensure(context.Background(), &halfEnforcingStore{})
	if err == nil {
		t.Fatal("a backend that ignores If-Match was accepted")
	}
	if !errors.Is(err, ErrConditionalWritesUnsupported) {
		t.Fatalf("want ErrConditionalWritesUnsupported, got %v", err)
	}
}

// staticETagStore enforces both conditions but never changes its ETag, so
// If-Match can never detect a lost update.
type staticETagStore struct {
	halfEnforcingStore
}

func (s *staticETagStore) PutIfMatch(_ context.Context, key string, r io.Reader, ifMatch string) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.m[key]
	if ifMatch == "" && exists {
		return nil, ErrPreconditionFailed
	}
	if ifMatch != "" && ifMatch != "fixed" {
		return nil, ErrPreconditionFailed
	}
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s.m[key] = b
	return &Metadata{ETag: "fixed"}, nil
}

func TestProbeRejectsStaticETag(t *testing.T) {
	t.Parallel()
	var p CASProbe
	err := p.Ensure(context.Background(), &staticETagStore{})
	if !errors.Is(err, ErrConditionalWritesUnsupported) {
		t.Fatalf("a backend whose ETag never changes was accepted: %v", err)
	}
}

// TestProbeCleansUp checks the probe leaves nothing behind on a compliant
// backend.
func TestProbeCleansUp(t *testing.T) {
	t.Parallel()
	s := &compliantStore{}
	var p CASProbe
	if err := p.Ensure(context.Background(), s); err != nil {
		t.Fatalf("compliant backend rejected: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, casProbePrefix) {
			t.Errorf("probe object left behind: %q", k)
		}
	}
}

// compliantStore enforces both halves of the contract.
type compliantStore struct {
	mu      sync.Mutex
	m       map[string][]byte
	etags   map[string]string
	version int
}

func (s *compliantStore) Get(_ context.Context, key string) (io.ReadCloser, *Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return nil, nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), &Metadata{ETag: s.etags[key]}, nil
}

func (s *compliantStore) Put(_ context.Context, key string, r io.Reader) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(key, r)
}

func (s *compliantStore) writeLocked(key string, r io.Reader) (*Metadata, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if s.m == nil {
		s.m, s.etags = map[string][]byte{}, map[string]string{}
	}
	s.version++
	s.m[key] = b
	s.etags[key] = string(rune('a' + s.version%26))
	return &Metadata{ETag: s.etags[key]}, nil
}

func (s *compliantStore) PutIfMatch(_ context.Context, key string, r io.Reader, ifMatch string) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, exists := s.etags[key]
	if ifMatch == "" {
		if exists {
			return nil, ErrPreconditionFailed
		}
	} else if !exists || cur != ifMatch {
		return nil, ErrPreconditionFailed
	}
	return s.writeLocked(key, r)
}

func (s *compliantStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	delete(s.etags, key)
	return nil
}

func (s *compliantStore) List(_ context.Context, _ string) ([]string, error) { return nil, nil }
