package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/run"
)

type countingDriftProvider struct {
	acquires      atomic.Int32
	cleanups      atomic.Int32
	env           map[string]string
	secretEnvKeys []string
}

func (*countingDriftProvider) Name() string { return "shared" }
func (*countingDriftProvider) Type() string { return "test" }
func (p *countingDriftProvider) Acquire(context.Context) (*auth.Credential, error) {
	p.acquires.Add(1)
	env := p.env
	if env == nil {
		env = map[string]string{"DRIFT_TOKEN": "short-lived"}
	}
	return &auth.Credential{
		Env: env, SecretEnvKeys: p.secretEnvKeys, Kind: "test", Source: "shared",
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
	stateEnv, stateSecrets, resolver, rebinder, cleanup, err := buildDriftAuth(t.Context(), authCfg, engineCfg, registry, map[string]string{"HOME": "/tmp/reeve"})
	if err != nil {
		t.Fatal(err)
	}
	if stateEnv["HOME"] != "/tmp/reeve" || stateEnv["DRIFT_TOKEN"] != "short-lived" {
		t.Fatalf("state env = %v", stateEnv)
	}
	if _, ok := stateSecrets["short-lived"]; !ok {
		t.Fatalf("unclassified state credential must retain all-secret registration: %v", stateSecrets)
	}
	for _, ref := range []string{"api/prod", "worker/prod"} {
		env, _, release, err := resolver(t.Context(), ref)
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
	if _, _, release, err := rebinder(t.Context(), "api/prod"); err != nil {
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

func TestDriftStateLoginUsesNarrowSecretSet(t *testing.T) {
	const token = "state-login-secret-42"
	const region = "us-east-1"
	provider := &countingDriftProvider{
		env:           map[string]string{"DRIFT_TOKEN": token, "AWS_REGION": region},
		secretEnvKeys: []string{"DRIFT_TOKEN"},
	}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pulumi")
	script := "#!/bin/sh\nprintf 'token=%s region=%s\\n' \"$DRIFT_TOKEN\" \"$AWS_REGION\" >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	engineCfg := &schemas.Engine{Engine: schemas.EngineBody{
		Binary: schemas.EngineBinary{Path: binary},
		State:  schemas.EngineState{AuthProvider: "shared", URL: "test-backend"},
	}}
	stateEnv, stateSecrets, _, _, cleanup, err := buildDriftAuth(t.Context(), nil, engineCfg, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if _, ok := stateSecrets[token]; !ok {
		t.Fatalf("classified state token missing from secret set: %v", stateSecrets)
	}
	if _, ok := stateSecrets[region]; ok {
		t.Fatalf("region must not be registered as a credential: %v", stateSecrets)
	}

	err = run.PulumiLogin(t.Context(), engineCfg, stateEnv, stateSecrets)
	if err == nil {
		t.Fatal("expected fake state login failure")
	}
	if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("state login error leaked the classified token: %v", err)
	}
	if !strings.Contains(err.Error(), region) {
		t.Fatalf("state login error should keep the region readable: %v", err)
	}
}
