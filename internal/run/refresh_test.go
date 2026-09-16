package run

import (
	"context"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
)

type credentialRefreshEngine struct {
	stacks    []discovery.Stack
	refreshed []string
}

func (*credentialRefreshEngine) Name() string { return "test" }
func (*credentialRefreshEngine) Capabilities() iac.Capabilities {
	return iac.Capabilities{SupportsRefresh: true}
}
func (e *credentialRefreshEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return e.stacks, nil
}
func (*credentialRefreshEngine) Preview(context.Context, discovery.Stack, iac.PreviewOpts) (iac.PreviewResult, error) {
	return iac.PreviewResult{}, nil
}
func (e *credentialRefreshEngine) Refresh(_ context.Context, stack discovery.Stack, _ iac.RefreshOpts) (iac.RefreshResult, error) {
	e.refreshed = append(e.refreshed, stack.Ref())
	return iac.RefreshResult{Counts: summary.Counts{Change: 1}}, nil
}

func TestRefreshReusesCredentialAcrossStateAndStacks(t *testing.T) {
	provider := &countingCredentialProvider{}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	engine := &credentialRefreshEngine{stacks: []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
	}}
	out, err := Refresh(t.Context(), RefreshInput{
		Local: true, DryRun: true, RepoRoot: "/repo", Engine: engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "tofu", State: schemas.EngineState{AuthProvider: "shared"},
			Stacks: []schemas.StackDecl{
				{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
				{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
			},
		}},
		AuthConfig: &schemas.Auth{
			Providers: map[string]schemas.ProviderYAML{"shared": {Type: "test"}},
			Bindings: []schemas.BindingYAML{{
				Match: schemas.BindingMatch{Stack: "*/*", Mode: "apply"}, Providers: []string{"shared"},
			}},
		},
		AuthRegistry: registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed || out.Blocked || len(engine.refreshed) != 2 {
		t.Fatalf("refresh result = %+v, refreshed = %v", out, engine.refreshed)
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("credential acquisitions = %d, want 1", provider.acquires.Load())
	}
	if provider.cleanups.Load() != 1 {
		t.Fatalf("credential cleanups = %d, want 1", provider.cleanups.Load())
	}
}
