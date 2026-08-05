package run

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/vcs"
)

type fakeEngine struct {
	enum    []discovery.Stack
	results map[string]iac.PreviewResult
}

func (f *fakeEngine) Name() string                   { return "fake" }
func (f *fakeEngine) Capabilities() iac.Capabilities { return iac.Capabilities{} }
func (f *fakeEngine) EnumerateStacks(ctx context.Context, root string) ([]discovery.Stack, error) {
	return f.enum, nil
}
func (f *fakeEngine) Preview(ctx context.Context, s discovery.Stack, opts iac.PreviewOpts) (iac.PreviewResult, error) {
	if r, ok := f.results[s.Ref()]; ok {
		return r, nil
	}
	return iac.PreviewResult{}, nil
}

type fakeVCS struct {
	changed []string
	posted  string
	headSHA string
}

func (f *fakeVCS) ListChangedFiles(ctx context.Context, _ int) ([]string, error) {
	return f.changed, nil
}
func (f *fakeVCS) GetPR(ctx context.Context, _ int) (*vcs.PR, error) {
	return &vcs.PR{HeadSHA: f.headSHA}, nil
}
func (f *fakeVCS) UpsertComment(ctx context.Context, _ int, body, _ string) error {
	f.posted = body
	return nil
}
func (f *fakeVCS) PostComment(ctx context.Context, _ int, body string) error {
	f.posted = body
	return nil
}

func TestPreviewEndToEnd(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{
			{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"},
			{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
			{Project: "worker", Path: "services/worker", Name: "prod", Env: "prod"},
		},
		results: map[string]iac.PreviewResult{
			"api/prod":    {Counts: summary.Counts{Add: 2, Change: 1}, PlanSummary: "+ s3 bucket"},
			"worker/prod": {Counts: summary.Counts{Replace: 1}, PlanSummary: "± rds"},
		},
	}
	vcs := &fakeVCS{changed: []string{"projects/api/index.ts", "services/worker/go.mod"}}
	store, _ := filesystem.New(t.TempDir())

	out, err := Preview(ctx, PreviewInput{
		PRNumber:  42,
		CommitSHA: "abc12345xyz",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "pulumi",
			Stacks: []schemas.StackDecl{
				{Project: "api", Path: "projects/api", Stacks: []string{"dev", "prod"}},
				{Pattern: "services/*", Stacks: []string{"prod"}},
			},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      vcs,
		Comments: vcs,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// Both api stacks share projects/api; worker shares services/worker.
	// 3 affected: api/dev (no-op), api/prod (ready), worker/prod (ready).
	if len(out.Stacks) != 3 {
		t.Fatalf("expected 3 affected stacks, got %d: %+v", len(out.Stacks), out.Stacks)
	}
	if !strings.Contains(vcs.posted, "reeve") {
		t.Fatalf("expected comment posted via fakeVCS, got %q", vcs.posted)
	}
	if !strings.Contains(out.CommentBody, "api/prod") {
		t.Fatalf("comment missing api/prod: %s", out.CommentBody)
	}
}

func TestPlanSucceeded(t *testing.T) {
	tests := []struct {
		name   string
		stacks []summary.StackSummary
		want   bool
	}{
		{"empty", nil, false},
		{"all planned", []summary.StackSummary{
			{Status: summary.StatusPlanned},
			{Status: summary.StatusNoOp},
		}, true},
		{"one error", []summary.StackSummary{
			{Status: summary.StatusPlanned},
			{Status: summary.StatusError},
		}, false},
	}
	for _, tt := range tests {
		if got := planSucceeded(tt.stacks); got != tt.want {
			t.Errorf("%s: planSucceeded = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestPreviewSHAOverriddenFromPRHead verifies that Preview overwrites the
// env-derived CommitSHA with the PR head SHA before storing the manifest.
// On pull_request events $GITHUB_SHA is the ephemeral merge commit; apply
// always uses pr.HeadSHA, so the manifest must be keyed to the same SHA.
func TestPreviewSHAOverriddenFromPRHead(t *testing.T) {
	ctx := context.Background()
	const envSHA = "merge-commit-sha"
	const headSHA = "pr-head-sha"

	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	fvcs := &fakeVCS{
		changed: []string{"projects/api/main.ts"},
		headSHA: headSHA,
	}
	store, _ := filesystem.New(t.TempDir())

	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: envSHA,
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// RunID embeds the short SHA -- must be from headSHA, not envSHA.
	if !strings.HasSuffix(out.RunID, shortSHA(headSHA)) {
		t.Errorf("RunID %q should end with shortSHA(%q)=%q", out.RunID, headSHA, shortSHA(headSHA))
	}

	// Manifest stored in bucket must be keyed to headSHA so apply can find it.
	found, err := FindPreviewForStack(ctx, store, 1, headSHA, "api/dev")
	if err != nil {
		t.Fatalf("FindPreviewForStack: %v", err)
	}
	if !found.Found {
		t.Error("manifest not found under headSHA -- SHA override did not apply")
	}

	// Must not be findable under the merge commit SHA.
	notFound, _ := FindPreviewForStack(ctx, store, 1, envSHA, "api/dev")
	if notFound.Found {
		t.Error("manifest found under envSHA -- SHA was not overridden")
	}
}

// TestPreviewIgnoreChanges verifies that files matching ignore_changes globs
// are stripped before stack matching -- a change only to an ignored path must
// not trigger any stack.
func TestPreviewIgnoreChanges(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	fvcs := &fakeVCS{
		// Only a docs change -- would normally not match, but also in ignore_changes.
		changed: []string{"projects/api/README.md"},
		headSHA: "head-sha",
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
			ChangeMapping: schemas.ChangeMap{
				IgnoreChanges: []string{"**/*.md"},
			},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stacks) != 0 {
		t.Fatalf("ignored file change should affect 0 stacks, got %d: %v", len(out.Stacks), out.Stacks)
	}
}

// TestPreviewExtraTriggers verifies that extra_triggers cause a project to run
// preview even when its own stack path has no changed files.
func TestPreviewExtraTriggers(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	fvcs := &fakeVCS{
		// Change is in shared lib, not in projects/api -- but api has an extra trigger for it.
		changed: []string{"shared/lib/utils.ts"},
		headSHA: "head-sha",
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
			ChangeMapping: schemas.ChangeMap{
				ExtraTriggers: []schemas.ExtraTrigger{
					{Project: "api", Paths: []string{"shared/lib/**"}},
				},
			},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stacks) != 1 {
		t.Fatalf("extra_trigger should affect 1 stack, got %d", len(out.Stacks))
	}
	if out.Stacks[0].Stack != "dev" {
		t.Errorf("unexpected stack %q", out.Stacks[0].Stack)
	}
}

// TestPreviewExcludeFilter verifies that filters.exclude removes stacks from
// the enumeration before preview runs.
func TestPreviewExcludeFilter(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{
			{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"},
			{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		},
	}
	fvcs := &fakeVCS{
		changed: []string{"projects/api/index.ts"},
		headSHA: "head-sha",
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev", "prod"}}},
			Filters: schemas.EngineFilters{
				Exclude: []schemas.ExcludeRule{{Stack: "*/prod"}},
			},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stacks) != 1 {
		t.Fatalf("exclude filter should leave 1 stack, got %d: %v", len(out.Stacks), out.Stacks)
	}
	if out.Stacks[0].Stack != "dev" {
		t.Errorf("expected dev stack, got %q", out.Stacks[0].Stack)
	}
}

// TestPreviewPatternDecl verifies that pattern-based stack declarations
// (glob over paths) match stacks correctly.
func TestPreviewPatternDecl(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{
			{Project: "svc-auth", Path: "services/auth", Name: "prod", Env: "prod"},
			{Project: "svc-billing", Path: "services/billing", Name: "prod", Env: "prod"},
			{Project: "infra", Path: "infra", Name: "prod", Env: "prod"},
		},
	}
	fvcs := &fakeVCS{
		changed: []string{"services/auth/main.go"},
		headSHA: "head-sha",
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{
				{Pattern: "services/*", Stacks: []string{"prod"}},
				{Project: "infra", Path: "infra", Stacks: []string{"prod"}},
			},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only services/auth changed -- svc-auth/prod should be affected, not svc-billing or infra.
	if len(out.Stacks) != 1 {
		t.Fatalf("pattern decl should match 1 stack, got %d: %v", len(out.Stacks), out.Stacks)
	}
	if out.Stacks[0].Project != "svc-auth" {
		t.Errorf("expected svc-auth project, got %q", out.Stacks[0].Project)
	}
}

func TestPreviewLocalIgnoresChangedFiles(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		Local:  true,
		Engine: engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared: &schemas.Shared{},
		Blob:   store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stacks) != 1 {
		t.Fatalf("local mode should run all declared stacks, got %d", len(out.Stacks))
	}
}

// TestPreviewLocalSkipsSHAOverride verifies that --local mode (no VCS) does
// not attempt SHA override and uses CommitSHA as-is.
func TestPreviewLocalSkipsSHAOverride(t *testing.T) {
	ctx := context.Background()
	const sha = "local-sha"
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		Local:     true,
		PRNumber:  1,
		CommitSHA: sha,
		RunNumber: 1,
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared: &schemas.Shared{},
		Blob:   store,
		// VCS intentionally nil -- local mode must not call GetPR
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.RunID, shortSHA(sha)) {
		t.Errorf("RunID %q should end with shortSHA(%q)=%q", out.RunID, sha, shortSHA(sha))
	}
}

// TestPreviewNoAffectedStacks verifies that when no changed files match any
// stack, an empty manifest is written and no stacks are returned.
func TestPreviewNoAffectedStacks(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	fvcs := &fakeVCS{
		changed: []string{"docs/README.md"}, // matches no stack path
		headSHA: "head-sha",
	}
	store, _ := filesystem.New(t.TempDir())
	out, err := Preview(ctx, PreviewInput{
		PRNumber:  1,
		CommitSHA: "head-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fvcs,
		Comments: fvcs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stacks) != 0 {
		t.Fatalf("expected 0 affected stacks, got %d", len(out.Stacks))
	}
	// FindPreviewForStack must return Found=false when stack not in manifest.
	status, err := FindPreviewForStack(ctx, store, 1, "head-sha", "api/dev")
	if err != nil {
		t.Fatalf("FindPreviewForStack: %v", err)
	}
	if status.Found {
		t.Error("stack should not be found in manifest when not affected")
	}
}
