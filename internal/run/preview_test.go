package run

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
	reevelog "github.com/reeveops/reeve/internal/log"
	"github.com/reeveops/reeve/internal/vcs"
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
	if !out.CommentPosted {
		t.Fatal("preview did not report the posted comment")
	}
}

func TestPreviewFailedRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stacks []summary.StackSummary
		want   []string
	}{
		{"empty", nil, nil},
		{"all planned", []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
			{Project: "web", Stack: "prod", Status: summary.StatusNoOp},
		}, nil},
		{"one error", []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusPlanned},
			{Project: "web", Stack: "prod", Status: summary.StatusError},
		}, []string{"web/prod"}},
		{"multiple errors preserve run order", []summary.StackSummary{
			{Project: "api", Stack: "prod", Status: summary.StatusError},
			{Project: "web", Stack: "prod", Status: summary.StatusNoOp},
			{Project: "worker", Stack: "prod", Status: summary.StatusError},
		}, []string{"api/prod", "worker/prod"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := previewFailedRefs(tt.stacks); !slices.Equal(got, tt.want) {
				t.Errorf("previewFailedRefs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreviewFailurePreservesOutputAndArtifacts(t *testing.T) {
	ctx := context.Background()
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
		results: map[string]iac.PreviewResult{
			"api/prod": {Error: "engine preview failed"},
		},
	}
	fvcs := &fakeVCS{changed: []string{"projects/api/main.ts"}, headSHA: "head-sha"}
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	out, err := Preview(ctx, PreviewInput{
		PRNumber: 1, CommitSHA: "head-sha", RunNumber: 7, RepoRoot: "/nope",
		Engine: engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared: &schemas.Shared{}, Blob: store, VCS: fvcs, Comments: fvcs,
	})
	if err == nil {
		t.Fatal("Preview returned nil error for a failed stack")
	}
	if out == nil {
		t.Fatal("Preview discarded output for a failed stack")
	}
	if !out.Failed || !slices.Equal(out.FailedStacks, []string{"api/prod"}) {
		t.Fatalf("failure metadata = failed:%v stacks:%v", out.Failed, out.FailedStacks)
	}
	if !out.CommentPosted {
		t.Fatal("Preview did not report the posted failure comment")
	}
	if !strings.Contains(out.CommentBody, "engine preview failed") {
		t.Fatalf("failure comment missing engine error: %s", out.CommentBody)
	}
	found, findErr := FindPreviewForStack(ctx, store, 1, "head-sha", "api/prod")
	if findErr != nil {
		t.Fatal(findErr)
	}
	if !found.Found || found.Succeeded || found.Plan == nil || found.Plan.Status != summary.StatusError {
		t.Fatalf("failed preview artifact = %+v", found)
	}
}

func TestRunPreviewOneRedactsAuthFailureLog(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	reevelog.Install(&logs, slog.LevelDebug, reevelog.FormatText)

	secret := "gho_" + strings.Repeat("a", 40)
	reg := auth.NewRegistry()
	if err := reg.Register(&fakeProvider{
		name: "gcp-prod", typ: "gcp_wif", err: fmt.Errorf("exchange rejected token %s", secret),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := localAuthCfg()
	stack := discovery.Stack{Project: "prod", Name: "api", Env: "prod"}

	got := runPreviewOne(context.Background(), PreviewInput{
		Shared: &schemas.Shared{}, AuthConfig: cfg, AuthRegistry: reg,
	}, nil, stack, "run-1", nil)
	if got.Status != summary.StatusError {
		t.Fatalf("status = %s, want error", got.Status)
	}
	if strings.Contains(got.Error, secret) || !strings.Contains(got.Error, "[redacted]") {
		t.Fatalf("summary error was not redacted: %q", got.Error)
	}
	if strings.Contains(logs.String(), secret) || !strings.Contains(logs.String(), "[redacted]") {
		t.Fatalf("auth failure log was not redacted: %s", logs.String())
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
	if out.CommentPosted {
		t.Fatal("local preview reported a posted comment")
	}
}

func TestPreviewLocalRejectsPRNumber(t *testing.T) {
	// A local run keyed to a real PR could write a preview manifest that
	// apply's freshness gate trusts. Refused before any work happens.
	store, _ := filesystem.New(t.TempDir())
	_, err := Preview(context.Background(), PreviewInput{
		Local:    true,
		PRNumber: 7,
		Engine: &fakeEngine{
			enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
		},
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared: &schemas.Shared{},
		Blob:   store,
	})
	if err == nil || !strings.Contains(err.Error(), "must not carry a PR number") {
		t.Fatalf("local preview with a PR number must be refused, got: %v", err)
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
