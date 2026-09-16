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

type credentialCacheEntry struct {
	credential *Credential
	acquiring  bool
	ready      chan struct{}
}

// CredentialCache reuses provider credentials within one command invocation.
// Close must run after every consumer has stopped using returned env values.
type CredentialCache struct {
	registry *Registry

	mu           sync.Mutex
	entries      map[string]*credentialCacheEntry
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
		if entry.acquiring {
			ready := entry.ready
			c.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		entry.acquiring = true
		entry.ready = make(chan struct{})
		c.mu.Unlock()

		credential, err := provider.Acquire(ctx)
		if err == nil && credential == nil {
			err = fmt.Errorf("provider returned nil credential")
		}
		if err == nil && !credentialUsable(credential, c.now(), c.safetyMargin) {
			err = fmt.Errorf("credential expires within the %s safety margin", c.safetyMargin)
		}

		c.mu.Lock()
		entry.acquiring = false
		if err == nil && !c.closed {
			entry.credential = credential
			c.owned = append(c.owned, credential)
		}
		close(entry.ready)
		closed := c.closed
		c.mu.Unlock()

		if closed {
			return nil, errors.Join(ErrCredentialCacheClosed, cleanupCredential(credential))
		}
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("acquire %s (%s): %w", name, provider.Type(), err),
				cleanupCredential(credential),
			)
		}
		return cloneCredential(credential), nil
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
