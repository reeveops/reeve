package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/audit"
	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/summary"
)

// trigApplyInput builds a NON-break-glass apply input whose gates all pass
// (one valid approval, checks green, a successful preview seeded) so that a
// matching trigger source reaches the engine. The trigger source and the
// configured apply.trigger mode are supplied by the caller.
func trigApplyInput(t *testing.T, engine *bgEngine, fv *bgVCS, mode, source string, store blob.Store) ApplyInput {
	t.Helper()
	if err := writeManifest(context.Background(), store, 21, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	shared := &schemas.Shared{
		Bucket: schemas.BucketConfig{Type: "filesystem"},
		Apply:  schemas.ApplyConfig{Trigger: mode},
	}
	return ApplyInput{
		PRNumber:      21,
		TriggerSource: source,
		CommitSHA:     bgSHA,
		RunNumber:     3,
		CIRunURL:      "https://ci.example/run/3",
		RepoRoot:      "/nope",
		RepoFull:      "org/repo",
		Actor:         "alice",
		Engine:        engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type:   "pulumi",
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared:      shared,
		Blob:        store,
		Locks:       blocks.New(store),
		VCS:         fv,
		AuditWriter: audit.NewWriter(store),
	}
}

// trigFixture returns an engine + a VCS carrying one valid, fresh approval on
// the current HEAD SHA so the approvals gate is satisfied.
func trigFixture() (*bgEngine, *bgVCS) {
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{
		changed:     []string{"projects/api/main.ts"},
		headSHA:     bgSHA,
		repoPrivate: true, // a single review approval only gates a private repo
		approvalsList: []approvals.Approval{
			{Source: "pr_review", Approver: "reviewer", CommitSHA: bgSHA, Pinned: true},
		},
	}
	return engine, fv
}

// TestApplyTriggerComment_DefaultApplies confirms the default (comment) mode
// applies on a comment-sourced apply and is a no-op on a merge-sourced one.
func TestApplyTriggerComment(t *testing.T) {
	t.Run("comment source applies", func(t *testing.T) {
		engine, fv := trigFixture()
		store, _ := filesystem.New(t.TempDir())
		in := trigApplyInput(t, engine, fv, "comment", "comment", store)
		in.CommitSHA = strings.Repeat("f", 40)
		out, err := Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Blocked {
			t.Fatalf("comment apply in comment mode must not be blocked: %+v", out.Stacks)
		}
		if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
			t.Fatalf("engine.Apply not invoked: %v", engine.applied)
		}
		if fv.getPRCalls != 1 {
			t.Fatalf("PR metadata reads = %d, want 1", fv.getPRCalls)
		}
		if out.RunID != runIdentity("apply", 3, 0, bgSHA) {
			t.Fatalf("run ID %q does not use authoritative PR head %s", out.RunID, bgSHA)
		}
	})

	t.Run("merge source is a no-op", func(t *testing.T) {
		engine, fv := trigFixture()
		store, _ := filesystem.New(t.TempDir())
		in := trigApplyInput(t, engine, fv, "comment", "merge", store)
		out, err := Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(engine.applied) != 0 {
			t.Fatalf("merge source in comment mode must apply nothing: %v", engine.applied)
		}
		if len(out.Stacks) != 0 {
			t.Fatalf("no-op must return no stack summaries: %+v", out.Stacks)
		}
		if fv.allComments() != "" {
			t.Fatalf("no-op must not post any PR comment: %q", fv.allComments())
		}
		if fv.getPRCalls != 0 {
			t.Fatalf("trigger mismatch read PR metadata %d times", fv.getPRCalls)
		}
	})

	// Empty trigger source defaults to comment (backward compat for callers
	// that never set the flag).
	t.Run("empty source defaults to comment", func(t *testing.T) {
		engine, fv := trigFixture()
		store, _ := filesystem.New(t.TempDir())
		in := trigApplyInput(t, engine, fv, "", "", store)
		if _, err := Apply(context.Background(), in); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(engine.applied) != 1 {
			t.Fatalf("empty source in default mode must apply: %v", engine.applied)
		}
	})
}

// TestApplyTriggerMerge confirms merge mode applies on a merge-sourced apply
// and is a no-op on a comment-sourced one.
func TestApplyTriggerMerge(t *testing.T) {
	t.Run("merge source applies", func(t *testing.T) {
		engine, fv := trigFixture()
		store, _ := filesystem.New(t.TempDir())
		in := trigApplyInput(t, engine, fv, "merge", "merge", store)
		out, err := Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Blocked {
			t.Fatalf("merge apply in merge mode must not be blocked: %+v", out.Stacks)
		}
		if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
			t.Fatalf("engine.Apply not invoked on merge: %v", engine.applied)
		}
	})

	t.Run("comment source is a no-op", func(t *testing.T) {
		engine, fv := trigFixture()
		store, _ := filesystem.New(t.TempDir())
		in := trigApplyInput(t, engine, fv, "merge", "comment", store)
		out, err := Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(engine.applied) != 0 {
			t.Fatalf("comment source in merge mode must apply nothing: %v", engine.applied)
		}
		if len(out.Stacks) != 0 {
			t.Fatalf("no-op must return no stack summaries: %+v", out.Stacks)
		}
	})
}

// TestApplyTriggerMergeStillEnforcesApprovals proves that switching to merge
// mode does NOT weaken any gate: an unapproved stack is NOT applied on merge.
func TestApplyTriggerMergeStillEnforcesApprovals(t *testing.T) {
	engine, fv := trigFixture()
	fv.approvalsList = nil // no approvals -> approvals gate fails closed
	store, _ := filesystem.New(t.TempDir())
	in := trigApplyInput(t, engine, fv, "merge", "merge", store)

	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatal("unapproved stack must be blocked even on merge")
	}
	if len(engine.applied) != 0 {
		t.Fatalf("nothing may apply without approval on merge: %v", engine.applied)
	}
}

func TestApplyRunIdentityUsesAuthoritativeHeadAndAttempt(t *testing.T) {
	engine, fv := trigFixture()
	store, _ := filesystem.New(t.TempDir())
	in := trigApplyInput(t, engine, fv, "comment", "comment", store)
	in.CommitSHA = strings.Repeat("f", 40)
	in.RunAttempt = 2

	out, err := Apply(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := runIdentity("apply", 3, 2, bgSHA)
	if out.RunID != want {
		t.Fatalf("run ID = %q, want %q", out.RunID, want)
	}
}

func TestApplyBlockedGateDoesNotRunPolicyHook(t *testing.T) {
	engine, fv := trigFixture()
	fv.approvalsList = nil
	store, _ := filesystem.New(t.TempDir())
	in := trigApplyInput(t, engine, fv, "comment", "comment", store)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	in.Config.Engine.PolicyHooks = []schemas.PolicyHookYAML{{
		Name: "marker", Command: []string{"touch", marker},
	}}
	loginMarker := filepath.Join(t.TempDir(), "login-ran")
	loginBinary := filepath.Join(t.TempDir(), "fake-pulumi")
	if err := os.WriteFile(loginBinary, []byte("#!/bin/sh\ntouch \""+loginMarker+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	in.Config.Engine.Binary.Path = loginBinary
	in.Config.Engine.State.URL = "test-backend"
	stateAcquired := false
	in.Config.Engine.State.AuthProvider = "state"
	in.AuthRegistry = auth.NewRegistry()
	if err := in.AuthRegistry.Register(&fakeProvider{
		name: "state", typ: "test", acquired: &stateAcquired,
	}); err != nil {
		t.Fatal(err)
	}

	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatal("unapproved stack must be blocked")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("policy hook ran before independent gates passed: %v", err)
	}
	if _, err := os.Stat(loginMarker); !os.IsNotExist(err) {
		t.Fatalf("backend login ran before independent gates passed: %v", err)
	}
	if stateAcquired {
		t.Fatal("state credentials were acquired before independent gates passed")
	}
}

func TestApplyReusesCredentialAcrossStateAndStacks(t *testing.T) {
	provider := &countingCredentialProvider{}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	stacks := []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
	}
	engine := &bgEngine{enum: stacks}
	fv := &bgVCS{
		changed: []string{"projects/api/main.ts", "projects/worker/main.ts"}, headSHA: bgSHA, repoPrivate: true,
		approvalsList: []approvals.Approval{{Source: "pr_review", Approver: "reviewer", CommitSHA: bgSHA, Pinned: true}},
	}
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(t.Context(), store, 21, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	in := ApplyInput{
		PRNumber: 21, TriggerSource: "comment", CommitSHA: bgSHA, RunNumber: 3,
		CIRunURL: "https://ci.example/run/3", RepoRoot: "/nope", RepoFull: "org/repo", Actor: "alice",
		Engine: engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "pulumi", State: schemas.EngineState{AuthProvider: "shared"},
			Stacks: []schemas.StackDecl{
				{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
				{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
			},
		}},
		Shared: &schemas.Shared{Bucket: schemas.BucketConfig{Type: "filesystem"}},
		AuthConfig: &schemas.Auth{
			Providers: map[string]schemas.ProviderYAML{"shared": {Type: "test"}},
			Bindings: []schemas.BindingYAML{{
				Match: schemas.BindingMatch{Stack: "*/*", Mode: "apply"}, Providers: []string{"shared"},
			}},
		},
		AuthRegistry: registry, Blob: store, Locks: blocks.New(store), VCS: fv, AuditWriter: audit.NewWriter(store),
	}
	out, err := Apply(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Failed || out.Blocked || len(engine.applied) != 2 {
		t.Fatalf("apply result = %+v, applied = %v", out, engine.applied)
	}
	if provider.acquires.Load() != 1 {
		t.Fatalf("credential acquisitions = %d, want 1", provider.acquires.Load())
	}
	if provider.cleanups.Load() != 1 {
		t.Fatalf("credential cleanups = %d, want 1", provider.cleanups.Load())
	}
}

// TestApplyTriggerMergeStillEnforcesLocks proves a lock held by another PR
// blocks a merge-mode apply.
func TestApplyTriggerMergeStillEnforcesLocks(t *testing.T) {
	ctx := context.Background()
	engine, fv := trigFixture()
	store, _ := filesystem.New(t.TempDir())
	in := trigApplyInput(t, engine, fv, "merge", "merge", store)

	if _, acquired, err := in.Locks.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 999, RunID: "other"}, time.Hour); err != nil || !acquired {
		t.Fatalf("seed lock: acquired=%v err=%v", acquired, err)
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatal("merge apply must be blocked by another PR's lock")
	}
	if len(engine.applied) != 0 {
		t.Fatalf("nothing may apply while locked on merge: %v", engine.applied)
	}
}

// TestApplyTriggerMergeStillEnforcesFreeze proves an active freeze window
// blocks a merge-mode apply.
func TestApplyTriggerMergeStillEnforcesFreeze(t *testing.T) {
	engine, fv := trigFixture()
	store, _ := filesystem.New(t.TempDir())
	in := trigApplyInput(t, engine, fv, "merge", "merge", store)
	// Fires hourly, lasts two hours: always active.
	in.Shared.FreezeWindows = []schemas.FreezeWindowYAML{{Name: "always", Cron: "0 * * * *", Duration: "2h"}}

	out, err := Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatal("merge apply must be blocked by an active freeze window")
	}
	if len(engine.applied) != 0 {
		t.Fatalf("nothing may apply during a freeze on merge: %v", engine.applied)
	}
}
