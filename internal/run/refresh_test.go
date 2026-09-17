package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/vcs"
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

type countingRefreshVCS struct {
	headSHA    string
	headSHAs   []string
	getPRCalls int
}

func (*countingRefreshVCS) ListChangedFiles(context.Context, int) ([]string, error) {
	return nil, nil
}
func (v *countingRefreshVCS) GetPR(context.Context, int) (*vcs.PR, error) {
	v.getPRCalls++
	headSHA := v.headSHA
	if len(v.headSHAs) > 0 {
		index := v.getPRCalls - 1
		if index >= len(v.headSHAs) {
			index = len(v.headSHAs) - 1
		}
		headSHA = v.headSHAs[index]
	}
	return &vcs.PR{Number: 7, HeadSHA: headSHA, BaseRef: "main"}, nil
}
func (*countingRefreshVCS) UpsertComment(context.Context, int, string, string) error { return nil }
func (*countingRefreshVCS) PostComment(context.Context, int, string) error           { return nil }

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

func TestRefreshUsesOneAuthoritativePRSnapshot(t *testing.T) {
	headSHA := strings.Repeat("a", 40)
	vcsClient := &countingRefreshVCS{headSHA: headSHA}
	engine := &credentialRefreshEngine{stacks: []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
	}}
	out, err := Refresh(t.Context(), RefreshInput{
		PRNumber: 7, CommitSHA: strings.Repeat("f", 40), RunNumber: 12, RunAttempt: 2,
		RepoRoot: "/repo", Engine: engine, VCS: vcsClient, All: true, DryRun: true,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "tofu", Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared: &schemas.Shared{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if vcsClient.getPRCalls != 1 {
		t.Fatalf("PR metadata reads = %d, want 1", vcsClient.getPRCalls)
	}
	want := runIdentity("refresh", 12, 2, headSHA)
	if out.RunID != want {
		t.Fatalf("run ID = %q, want %q", out.RunID, want)
	}
}

func TestRefreshRerunRefusesEarlierAttemptLock(t *testing.T) {
	t.Parallel()
	const sha = "abcdef1234567890"
	tests := []struct {
		name       string
		runAttempt int
	}{
		{name: "legacy attempt", runAttempt: 0},
		{name: "provider rerun", runAttempt: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, err := filesystem.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			lockStore := blocks.New(store)
			holder := corelocks.Holder{PR: 7, CommitSHA: sha, RunID: runIdentity("refresh", 12, 1, sha)}
			if _, acquired, err := lockStore.TryAcquire(t.Context(), "api", "prod", holder, time.Hour); err != nil || !acquired {
				t.Fatalf("initial acquire = (%t, %v), want success", acquired, err)
			}
			engine := &credentialRefreshEngine{stacks: []discovery.Stack{
				{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
			}}
			out, err := Refresh(t.Context(), RefreshInput{
				PRNumber: 7, CommitSHA: sha, RunNumber: 12, RunAttempt: tt.runAttempt,
				Local: true, RepoRoot: t.TempDir(), Engine: engine, Locks: lockStore,
				Config: &schemas.Engine{Engine: schemas.EngineBody{
					Type: "tofu", Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
				}},
				Shared: &schemas.Shared{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if out.Blocked || !out.Failed || len(engine.refreshed) != 0 {
				t.Fatalf("refresh rerun = %+v, refreshed = %v; want failed before engine execution", out, engine.refreshed)
			}
			if want := runIdentity("refresh", 12, tt.runAttempt, sha); out.RunID != want {
				t.Fatalf("run ID = %q, want %q", out.RunID, want)
			}
		})
	}
}

func TestRefreshRevalidatesExpectedHeadBeforeStateChange(t *testing.T) {
	expected := strings.Repeat("a", 40)
	moved := strings.Repeat("b", 40)
	vcsClient := &countingRefreshVCS{headSHAs: []string{expected, moved}}
	engine := &credentialRefreshEngine{stacks: []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
	}}
	_, err := Refresh(t.Context(), RefreshInput{
		PRNumber: 7, CommitSHA: expected, ExpectedHeadSHA: expected,
		RepoRoot: "/repo", Engine: engine, VCS: vcsClient, All: true,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "tofu", Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared: &schemas.Shared{},
	})
	if !errors.Is(err, ErrPRHeadMismatch) {
		t.Fatalf("Refresh error = %v, want %v", err, ErrPRHeadMismatch)
	}
	if vcsClient.getPRCalls != 2 {
		t.Fatalf("PR metadata reads = %d, want initial snapshot plus boundary revalidation", vcsClient.getPRCalls)
	}
	if len(engine.refreshed) != 0 {
		t.Fatalf("engine refreshed after PR head moved: %v", engine.refreshed)
	}
}
