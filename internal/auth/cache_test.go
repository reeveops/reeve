package auth

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cacheProvider struct {
	name      string
	acquires  atomic.Int32
	cleanups  atomic.Int32
	expiresAt time.Time
	started   chan struct{}
	release   chan struct{}
	err       error
}

func (p *cacheProvider) Name() string { return p.name }
func (p *cacheProvider) Type() string { return "cache-test" }
func (p *cacheProvider) Acquire(ctx context.Context) (*Credential, error) {
	acquisition := p.acquires.Add(1)
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return &Credential{
		Env:       map[string]string{"TOKEN": p.name, "GENERATION": strconv.Itoa(int(acquisition))},
		Kind:      "test",
		Source:    p.name,
		ExpiresAt: p.expiresAt,
		Cleanup: func() error {
			p.cleanups.Add(1)
			return nil
		},
	}, nil
}

func cacheWithProvider(t *testing.T, provider Provider) *CredentialCache {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	return NewCredentialCache(registry)
}

func TestCredentialCacheReusesAndClones(t *testing.T) {
	provider := &cacheProvider{name: "shared"}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })

	first, firstCreds, err := cache.AcquireAll(t.Context(), []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	first["TOKEN"] = "mutated"
	firstCreds[0].Env["GENERATION"] = "mutated"
	second, secondCreds, err := cache.AcquireAll(t.Context(), []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("provider acquisitions = %d, want 1", provider.acquires.Load())
	}
	if second["TOKEN"] != "shared" || secondCreds[0].Env["GENERATION"] != "1" {
		t.Fatalf("cached credential was mutated through a caller copy: env=%v credential=%v", second, secondCreds[0].Env)
	}
	if secondCreds[0].Cleanup != nil {
		t.Fatal("caller received ownership of the cache cleanup")
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if provider.cleanups.Load() != 1 {
		t.Fatalf("provider cleanups = %d, want 1", provider.cleanups.Load())
	}
}

func TestCredentialCacheCollapsesConcurrentAcquisition(t *testing.T) {
	provider := &cacheProvider{
		name: "shared", started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := cache.AcquireAll(t.Context(), []string{"shared"})
			errs <- err
		}()
	}
	<-provider.started
	if provider.acquires.Load() != 1 {
		t.Fatalf("provider acquired %d times before release", provider.acquires.Load())
	}
	waitForCredentialWaiters(t, cache, "shared", callers-1)
	close(provider.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("concurrent provider acquisitions = %d, want 1", provider.acquires.Load())
	}
}

func TestCredentialCacheSharesConcurrentFailure(t *testing.T) {
	providerErr := errors.New("temporary outage")
	provider := &cacheProvider{
		name: "shared", started: make(chan struct{}, 1), release: make(chan struct{}), err: providerErr,
	}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := cache.AcquireAll(t.Context(), []string{"shared"})
			errs <- err
		}()
	}
	<-provider.started
	if provider.acquires.Load() != 1 {
		t.Fatalf("provider acquired %d times before release", provider.acquires.Load())
	}
	waitForCredentialWaiters(t, cache, "shared", callers-1)
	close(provider.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, providerErr) {
			t.Fatalf("concurrent acquisition error = %v, want %v", err, providerErr)
		}
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("failed concurrent provider acquisitions = %d, want 1", provider.acquires.Load())
	}

	provider.err = nil
	if _, _, err := cache.AcquireAll(t.Context(), []string{"shared"}); err != nil {
		t.Fatalf("later retry after shared failure: %v", err)
	}
	if provider.acquires.Load() != 2 {
		t.Fatalf("provider acquisitions after retry = %d, want 2", provider.acquires.Load())
	}
}

func waitForCredentialWaiters(t *testing.T, cache *CredentialCache, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		cache.mu.Lock()
		entry := cache.entries[name]
		joined := 0
		if entry != nil && entry.attempt != nil {
			joined = entry.attempt.joined
		}
		cache.mu.Unlock()
		if joined == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrent waiters joined = %d, want %d", joined, want)
		}
		runtime.Gosched()
	}
}

func TestCredentialCacheRefreshesNearExpiry(t *testing.T) {
	base := time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)
	now := base
	provider := &cacheProvider{name: "short", expiresAt: base.Add(time.Hour)}
	cache := cacheWithProvider(t, provider)
	cache.now = func() time.Time { return now }
	t.Cleanup(func() { _ = cache.Close() })

	if _, _, err := cache.AcquireAll(t.Context(), []string{"short"}); err != nil {
		t.Fatal(err)
	}
	now = base.Add(time.Hour - 20*time.Second)
	provider.expiresAt = now.Add(time.Hour)
	if _, _, err := cache.AcquireAll(t.Context(), []string{"short"}); err != nil {
		t.Fatal(err)
	}
	if provider.acquires.Load() != 2 {
		t.Fatalf("near-expiry credential acquisitions = %d, want 2", provider.acquires.Load())
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if provider.cleanups.Load() != 2 {
		t.Fatalf("credential generation cleanups = %d, want 2", provider.cleanups.Load())
	}
}

func TestCredentialCacheRejectsNewCredentialInsideSafetyMargin(t *testing.T) {
	now := time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)
	provider := &cacheProvider{name: "too-short", expiresAt: now.Add(20 * time.Second)}
	cache := cacheWithProvider(t, provider)
	cache.now = func() time.Time { return now }
	t.Cleanup(func() { _ = cache.Close() })
	_, _, err := cache.AcquireAll(t.Context(), []string{"too-short"})
	if !errors.Is(err, ErrCredentialExpiresSoon) {
		t.Fatalf("short credential must be rejected, got %v", err)
	}
	if provider.cleanups.Load() != 1 {
		t.Fatalf("rejected credential cleanups = %d, want 1", provider.cleanups.Load())
	}
}

func TestCredentialCacheDoesNotCacheFailures(t *testing.T) {
	provider := &cacheProvider{name: "flaky", err: errors.New("temporary outage")}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })
	if _, _, err := cache.AcquireAll(t.Context(), []string{"flaky"}); err == nil {
		t.Fatal("failed acquisition returned nil error")
	}
	provider.err = nil
	if _, _, err := cache.AcquireAll(t.Context(), []string{"flaky"}); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if provider.acquires.Load() != 2 {
		t.Fatalf("provider acquisitions = %d, want 2", provider.acquires.Load())
	}
}

func TestCredentialCachePreservesProviderPrecedence(t *testing.T) {
	registry := NewRegistry()
	for _, provider := range []Provider{&cacheProvider{name: "first"}, &cacheProvider{name: "second"}} {
		if err := registry.Register(provider); err != nil {
			t.Fatal(err)
		}
	}
	cache := NewCredentialCache(registry)
	t.Cleanup(func() { _ = cache.Close() })
	env, _, err := cache.AcquireAll(t.Context(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if env["TOKEN"] != "second" {
		t.Fatalf("later provider did not win: %v", env)
	}
}

func TestCredentialCacheRejectsUseAfterClose(t *testing.T) {
	cache := cacheWithProvider(t, &cacheProvider{name: "closed"})
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.AcquireAll(t.Context(), []string{"closed"}); !errors.Is(err, ErrCredentialCacheClosed) {
		t.Fatalf("acquire after Close = %v, want ErrCredentialCacheClosed", err)
	}
}

func TestCredentialCacheRequiresRegistry(t *testing.T) {
	cache := NewCredentialCache(nil)
	if _, _, err := cache.AcquireAll(t.Context(), []string{"missing"}); err == nil {
		t.Fatal("acquire without registry returned nil error")
	}
}

func TestCredentialCacheInvalidationAcquiresNewGeneration(t *testing.T) {
	provider := &cacheProvider{name: "shared"}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })
	first, _, err := cache.AcquireAll(t.Context(), []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.InvalidateAll(); err != nil {
		t.Fatal(err)
	}
	second, _, err := cache.AcquireAll(t.Context(), []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	if first["GENERATION"] != "1" || second["GENERATION"] != "2" {
		t.Fatalf("credential generations = %q then %q, want 1 then 2", first["GENERATION"], second["GENERATION"])
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if provider.cleanups.Load() != 2 {
		t.Fatalf("credential generation cleanups = %d, want 2", provider.cleanups.Load())
	}
}

func TestCredentialCacheInvalidationDiscardsInFlightGeneration(t *testing.T) {
	provider := &cacheProvider{
		name: "shared", started: make(chan struct{}, 2), release: make(chan struct{}),
	}
	cache := cacheWithProvider(t, provider)
	t.Cleanup(func() { _ = cache.Close() })

	result := make(chan map[string]string, 1)
	errs := make(chan error, 1)
	go func() {
		env, _, err := cache.AcquireAll(t.Context(), []string{"shared"})
		result <- env
		errs <- err
	}()

	<-provider.started
	if err := cache.InvalidateAll(); err != nil {
		t.Fatal(err)
	}
	close(provider.release)

	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	env := <-result
	if env["GENERATION"] != "2" {
		t.Fatalf("credential generation = %q, want post-invalidation generation 2", env["GENERATION"])
	}
	if provider.acquires.Load() != 2 {
		t.Fatalf("provider acquisitions = %d, want 2", provider.acquires.Load())
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if provider.cleanups.Load() != 2 {
		t.Fatalf("credential generation cleanups = %d, want 2", provider.cleanups.Load())
	}
}
