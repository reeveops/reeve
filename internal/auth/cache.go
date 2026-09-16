package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultCredentialSafetyMargin = 30 * time.Second

// ErrCredentialCacheClosed is returned when acquisition starts after Close.
var ErrCredentialCacheClosed = errors.New("credential cache is closed")

// ErrCredentialExpiresSoon is returned when a newly acquired credential does
// not remain valid beyond the cache safety margin.
var ErrCredentialExpiresSoon = errors.New("credential expires inside the safety margin")

type credentialCacheEntry struct {
	credential *Credential
	attempt    *credentialCacheAttempt
}

type credentialCacheAttempt struct {
	ready      chan struct{}
	credential *Credential
	err        error
	retry      bool
	joined     int
}

// CredentialCache reuses provider credentials within one command invocation.
// Close must run after every consumer has stopped using returned env values.
type CredentialCache struct {
	registry *Registry

	mu           sync.Mutex
	entries      map[string]*credentialCacheEntry
	generation   uint64
	owned        []*Credential
	closed       bool
	now          func() time.Time
	safetyMargin time.Duration
	closeOnce    sync.Once
	closeErr     error
}

// NewCredentialCache returns an invocation-scoped cache over registry.
func NewCredentialCache(registry *Registry) *CredentialCache {
	return &CredentialCache{
		registry:     registry,
		entries:      map[string]*credentialCacheEntry{},
		now:          time.Now,
		safetyMargin: defaultCredentialSafetyMargin,
	}
}

// AcquireAll resolves names in order and preserves later-provider override
// semantics. Concurrent requests for one provider share one acquisition.
func (c *CredentialCache) AcquireAll(ctx context.Context, names []string) (map[string]string, []*Credential, error) {
	merged := make(map[string]string)
	creds := make([]*Credential, 0, len(names))
	for _, name := range names {
		credential, err := c.acquireOne(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range credential.Env {
			merged[key] = value
		}
		creds = append(creds, credential)
	}
	return merged, creds, nil
}

func (c *CredentialCache) acquireOne(ctx context.Context, name string) (*Credential, error) {
	if c == nil || c.registry == nil {
		return nil, errors.New("credential cache has no registry")
	}
	provider, ok := c.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrCredentialCacheClosed
		}
		entry := c.entries[name]
		if entry == nil {
			entry = &credentialCacheEntry{}
			c.entries[name] = entry
		}
		if credentialUsable(entry.credential, c.now(), c.safetyMargin) {
			credential := cloneCredential(entry.credential)
			c.mu.Unlock()
			return credential, nil
		}
		if entry.attempt != nil {
			attempt := entry.attempt
			attempt.joined++
			c.mu.Unlock()
			select {
			case <-attempt.ready:
				if attempt.err != nil {
					return nil, attempt.err
				}
				if attempt.retry {
					continue
				}
				return cloneCredential(attempt.credential), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		attempt := &credentialCacheAttempt{ready: make(chan struct{})}
		entry.attempt = attempt
		startGeneration := c.generation
		c.mu.Unlock()

		credential, err := provider.Acquire(ctx)
		if err == nil && credential == nil {
			err = fmt.Errorf("provider returned nil credential")
		}
		if err == nil && !credentialUsable(credential, c.now(), c.safetyMargin) {
			err = fmt.Errorf("%w: %s", ErrCredentialExpiresSoon, c.safetyMargin)
		}
		var resultErr error
		if err != nil {
			resultErr = errors.Join(
				fmt.Errorf("acquire %s (%s): %w", name, provider.Type(), err),
				cleanupCredential(credential),
			)
			credential = nil
		}

		c.mu.Lock()
		stale := c.generation != startGeneration
		closed := c.closed
		if resultErr == nil && !closed {
			// The cache owns every successful acquisition even when an
			// invalidation raced it. Close releases discarded generations.
			c.owned = append(c.owned, credential)
		}
		if resultErr == nil && !closed && !stale {
			entry.credential = credential
		}
		if closed {
			c.mu.Unlock()
			resultErr = errors.Join(ErrCredentialCacheClosed, resultErr, cleanupCredential(credential))
			credential = nil
			c.mu.Lock()
		}
		attempt.err = resultErr
		attempt.retry = resultErr == nil && stale
		if resultErr == nil && !stale {
			attempt.credential = credential
		}
		if entry.attempt == attempt {
			entry.attempt = nil
		}
		close(attempt.ready)
		c.mu.Unlock()

		if attempt.err != nil {
			return nil, attempt.err
		}
		if attempt.retry {
			continue
		}
		return cloneCredential(attempt.credential), nil
	}
}

func cleanupCredential(credential *Credential) error {
	if credential == nil || credential.Cleanup == nil {
		return nil
	}
	if err := credential.Cleanup(); err != nil {
		return fmt.Errorf("cleanup %s: %w", credential.Source, err)
	}
	return nil
}

func credentialUsable(credential *Credential, now time.Time, safetyMargin time.Duration) bool {
	if credential == nil {
		return false
	}
	return credential.ExpiresAt.IsZero() || credential.ExpiresAt.After(now.Add(safetyMargin))
}

func cloneCredential(credential *Credential) *Credential {
	env := make(map[string]string, len(credential.Env))
	for key, value := range credential.Env {
		env[key] = value
	}
	return &Credential{
		Env:       env,
		Kind:      credential.Kind,
		Source:    credential.Source,
		ExpiresAt: credential.ExpiresAt,
	}
}

// InvalidateAll forces later requests to acquire new provider generations.
// Existing consumers retain their isolated environment maps until they stop.
func (c *CredentialCache) InvalidateAll() error {
	if c == nil {
		return errors.New("credential cache is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCredentialCacheClosed
	}
	for _, entry := range c.entries {
		entry.credential = nil
	}
	c.generation++
	return nil
}

// Close cleans every credential generation acquired by the cache exactly once.
func (c *CredentialCache) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		owned := append([]*Credential(nil), c.owned...)
		c.mu.Unlock()

		var cleanupErrors []error
		for i := len(owned) - 1; i >= 0; i-- {
			credential := owned[i]
			if credential == nil || credential.Cleanup == nil {
				continue
			}
			if err := credential.Cleanup(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup %s: %w", credential.Source, err))
			}
		}
		c.closeErr = errors.Join(cleanupErrors...)
	})
	return c.closeErr
}
