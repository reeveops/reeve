package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type lifecycleProvider struct {
	name    string
	err     error
	cleanup func() error
}

func (p lifecycleProvider) Name() string { return p.name }
func (p lifecycleProvider) Type() string { return "test" }
func (p lifecycleProvider) Acquire(context.Context) (*Credential, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &Credential{Source: p.name, Env: map[string]string{p.name: "value"}, Cleanup: p.cleanup}, nil
}

func TestAcquireAllUnwindsPartialAcquisition(t *testing.T) {
	t.Parallel()

	var cleaned []string
	registry := NewRegistry()
	for _, provider := range []lifecycleProvider{
		{name: "first", cleanup: func() error { cleaned = append(cleaned, "first"); return nil }},
		{name: "second", cleanup: func() error { cleaned = append(cleaned, "second"); return nil }},
		{name: "fails", err: context.DeadlineExceeded},
	} {
		if err := registry.Register(provider); err != nil {
			t.Fatal(err)
		}
	}

	env, creds, err := registry.AcquireAll(context.Background(), []string{"first", "second", "fails"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("primary acquisition error lost: %v", err)
	}
	if env != nil || creds != nil {
		t.Fatalf("failed acquisition returned credentials: env=%v creds=%v", env, creds)
	}
	if want := []string{"second", "first"}; !reflect.DeepEqual(cleaned, want) {
		t.Fatalf("cleanup order = %v, want %v", cleaned, want)
	}
}

func TestAcquireAllReportsCleanupFailure(t *testing.T) {
	t.Parallel()

	cleanupErr := errors.New("cleanup failed")
	registry := NewRegistry()
	if err := registry.Register(lifecycleProvider{name: "first", cleanup: func() error { return cleanupErr }}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(lifecycleProvider{name: "fails", err: context.Canceled}); err != nil {
		t.Fatal(err)
	}

	_, _, err := registry.AcquireAll(context.Background(), []string{"first", "fails"})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("joined acquisition and cleanup errors not preserved: %v", err)
	}
}
