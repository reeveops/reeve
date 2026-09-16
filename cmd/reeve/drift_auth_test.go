package main

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
)

type countingDriftProvider struct {
	acquires atomic.Int32
	cleanups atomic.Int32
}

func (*countingDriftProvider) Name() string { return "shared" }
func (*countingDriftProvider) Type() string { return "test" }
func (p *countingDriftProvider) Acquire(context.Context) (*auth.Credential, error) {
	p.acquires.Add(1)
	return &auth.Credential{
		Env: map[string]string{"DRIFT_TOKEN": "short-lived"}, Kind: "test", Source: "shared",
		Cleanup: func() error { p.cleanups.Add(1); return nil },
	}, nil
}

func TestBuildDriftAuthReusesCredentialAcrossStateAndStacks(t *testing.T) {
	provider := &countingDriftProvider{}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	authCfg := &schemas.Auth{
		Providers: map[string]schemas.ProviderYAML{"shared": {Type: "test"}},
		Bindings: []schemas.BindingYAML{{
			Match: schemas.BindingMatch{Stack: "*/*", Mode: "drift"}, Providers: []string{"shared"},
		}},
	}
	engineCfg := &schemas.Engine{Engine: schemas.EngineBody{
		State: schemas.EngineState{AuthProvider: "shared"},
	}}
	stateEnv, resolver, rebinder, cleanup, err := buildDriftAuth(t.Context(), authCfg, engineCfg, registry, map[string]string{"HOME": "/tmp/reeve"})
	if err != nil {
		t.Fatal(err)
	}
	if stateEnv["HOME"] != "/tmp/reeve" || stateEnv["DRIFT_TOKEN"] != "short-lived" {
		t.Fatalf("state env = %v", stateEnv)
	}
	for _, ref := range []string{"api/prod", "worker/prod"} {
		env, release, err := resolver(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		if env["DRIFT_TOKEN"] != "short-lived" {
			t.Fatalf("%s env = %v", ref, env)
		}
		release()
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("credential acquisitions = %d, want 1", provider.acquires.Load())
	}
	if _, release, err := rebinder(t.Context(), "api/prod"); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if provider.acquires.Load() != 2 {
		t.Fatalf("credential acquisitions after rebind = %d, want 2", provider.acquires.Load())
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if provider.cleanups.Load() != 2 {
		t.Fatalf("credential cleanups = %d, want 2", provider.cleanups.Load())
	}
}
