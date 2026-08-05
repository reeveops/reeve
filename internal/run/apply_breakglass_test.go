package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/audit"
	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	blocks "github.com/FynxLabs/reeve/internal/blob/locks"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	corelocks "github.com/FynxLabs/reeve/internal/core/locks"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/vcs"

	// Register the timeline channels so the break_glass notify emission can be
	// observed through a configured timeline_github channel.
	_ "github.com/FynxLabs/reeve/internal/notify/channels/timeline"
)

// bgEngine is a fake applyEngine recording Apply invocations.
type bgEngine struct {
	enum    []discovery.Stack
	applied []string
}

func (f *bgEngine) Name() string                   { return "fake" }
func (f *bgEngine) Capabilities() iac.Capabilities { return iac.Capabilities{} }
func (f *bgEngine) EnumerateStacks(ctx context.Context, root string) ([]discovery.Stack, error) {
	return f.enum, nil
}
func (f *bgEngine) Preview(ctx context.Context, s discovery.Stack, opts iac.PreviewOpts) (iac.PreviewResult, error) {
	return iac.PreviewResult{}, nil
}
func (f *bgEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	f.applied = append(f.applied, s.Ref())
	return iac.ApplyResult{Counts: summary.Counts{Add: 1}}, nil
}

// bgVCS is a fake applyVCS with configurable approvals and CODEOWNERS.
// Break-glass tests leave approvalsList nil (approvals gate would fail).
type bgVCS struct {
	changed       []string
	headSHA       string
	codeowners    string
	repoPrivate   bool // reported as vcs.PR.RepoPrivate
	approvalsList []approvals.Approval
	comments      map[string][]string // marker → bodies (upserts)
	posted        []string            // plain PostComment bodies
}

func (f *bgVCS) ListChangedFiles(ctx context.Context, _ int) ([]string, error) {
	return f.changed, nil
}
func (f *bgVCS) GetPR(ctx context.Context, n int) (*vcs.PR, error) {
	return &vcs.PR{Number: n, HeadSHA: f.headSHA, BaseRef: "main", Author: "author", RepoPrivate: f.repoPrivate}, nil
}
func (f *bgVCS) UpsertComment(ctx context.Context, _ int, body, marker string) error {
	if f.comments == nil {
		f.comments = map[string][]string{}
	}
	f.comments[marker] = append(f.comments[marker], body)
	return nil
}
func (f *bgVCS) PostComment(ctx context.Context, _ int, body string) error {
	f.posted = append(f.posted, body)
	return nil
}
func (f *bgVCS) Capabilities() vcs.CommentCapabilities {
	return vcs.CommentCapabilities{SupportsEdit: true}
}
func (f *bgVCS) ChecksGreen(ctx context.Context, _ string, _ vcs.ChecksGreenOpts) (bool, []string, error) {
	return true, nil, nil
}
func (f *bgVCS) CompareBranches(ctx context.Context, _, _ string) (int, error) { return 0, nil }
func (f *bgVCS) Name() string                                                  { return "fake" }
func (f *bgVCS) ListApprovals(ctx context.Context, _ approvals.PR) ([]approvals.Approval, error) {
	return f.approvalsList, nil // nil in break-glass tests: gate would fail normally
}
func (f *bgVCS) ListCommentApprovals(ctx context.Context, _ approvals.PR, _ vcs.CommentApprovalConfig) ([]approvals.Approval, error) {
	return nil, nil
}
func (f *bgVCS) FetchCodeowners(ctx context.Context) (string, error) { return f.codeowners, nil }
func (f *bgVCS) ListTeamMembers(ctx context.Context, slug string) ([]string, error) {
	return nil, nil
}

func (f *bgVCS) allComments() string {
	var b strings.Builder
	for _, bodies := range f.comments {
		for _, body := range bodies {
			b.WriteString(body + "\n")
		}
	}
	return b.String()
}

const bgSHA = "abc1234def5678"

func bgShared(bg *schemas.BreakGlassYAML) *schemas.Shared {
	return &schemas.Shared{
		Bucket:     schemas.BucketConfig{Type: "filesystem"},
		BreakGlass: bg,
	}
}

func bgApplyInput(t *testing.T, engine *bgEngine, fv *bgVCS, shared *schemas.Shared, store blob.Store) ApplyInput {
	t.Helper()
	// Seed a successful preview manifest so preview_succeeded passes: apply
	// gates must be exercised, not short-circuited by a missing preview.
	err := writeManifest(context.Background(), store, 18, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA)
	if err != nil {
		t.Fatal(err)
	}
	return ApplyInput{
		PRNumber:  18,
		CommitSHA: bgSHA,
		RunNumber: 7,
		CIRunURL:  "https://ci.example/run/7",
		RepoRoot:  "/nope",
		RepoFull:  "org/repo",
		Actor:     "alice",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type:   "pulumi",
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared:      shared,
		Blob:        store,
		Locks:       blocks.New(store),
		VCS:         fv,
		AuditWriter: audit.NewWriter(store),
		BreakGlass:  &BreakGlassRequest{Justification: "prod is down"},
	}
}

func newBGFixture() (*bgEngine, *bgVCS) {
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	return engine, fv
}

// readAuditEntry returns the single COMPLETION audit entry, skipping the
// break-glass "-intent" entry written before the engine runs.
func readAuditEntry(t *testing.T, store blob.Store) audit.Entry {
	t.Helper()
	ctx := context.Background()
	keys, err := store.List(ctx, "audit/")
	if err != nil {
		t.Fatal(err)
	}
	var completion []string
	for _, k := range keys {
		if !strings.HasSuffix(k, "-intent.json") {
			completion = append(completion, k)
		}
	}
	if len(completion) != 1 {
		t.Fatalf("want exactly one completion audit entry, got %v (all: %v)", completion, keys)
	}
	data, _, err := filesystem.ReadBytes(ctx, store, completion[0])
	if err != nil {
		t.Fatal(err)
	}
	var e audit.Entry
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// readIntentEntry returns the break-glass intent audit entry, failing the
// test if it is absent.
func readIntentEntry(t *testing.T, store blob.Store) audit.Entry {
	t.Helper()
	ctx := context.Background()
	keys, err := store.List(ctx, "audit/")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.HasSuffix(k, "-intent.json") {
			data, _, rerr := filesystem.ReadBytes(ctx, store, k)
			if rerr != nil {
				t.Fatal(rerr)
			}
			var e audit.Entry
			if err := json.Unmarshal(data, &e); err != nil {
				t.Fatal(err)
			}
			return e
		}
	}
	t.Fatalf("no break-glass intent audit entry found in %v", keys)
	return audit.Entry{}
}

func TestBreakGlassApplyOverridesApprovals(t *testing.T) {
	ctx := context.Background()
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
	}), store)
	in.Notifications = &schemas.Notifications{
		Header:   schemas.Header{Version: 2, ConfigType: "notifications"},
		Channels: []schemas.ChannelYAML{{Type: "timeline_github", On: []string{"break_glass"}}},
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Blocked {
		t.Fatalf("break-glass run must not be blocked by approvals: %+v", out.Stacks)
	}
	if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
		t.Fatalf("engine.Apply not invoked as expected: %v", engine.applied)
	}

	// Loud comment: marker + admonition + justification quoted.
	all := fv.allComments()
	for _, want := range []string{"reeve:break-glass:v1", "[!WARNING]", "BREAK-GLASS APPLY", "prod is down", "`internal_list`", "`approvals`"} {
		if !strings.Contains(all, want) {
			t.Fatalf("PR comments missing %q:\n%s", want, all)
		}
	}
	// The timeline channel saw the break_glass notify event.
	if !strings.Contains(all, "break-glass override") {
		t.Fatalf("timeline break_glass entry missing:\n%s", all)
	}

	// Audit record.
	e := readAuditEntry(t, store)
	if e.BreakGlass == nil {
		t.Fatal("audit entry missing break_glass block")
	}
	if e.BreakGlass.Justification != "prod is down" || e.BreakGlass.AuthorizedVia != "internal_list" {
		t.Fatalf("audit break_glass mismatch: %+v", e.BreakGlass)
	}
	if len(e.BreakGlass.OverriddenGates) != 1 || e.BreakGlass.OverriddenGates[0] != "approvals" {
		t.Fatalf("overridden gates mismatch: %+v", e.BreakGlass.OverriddenGates)
	}
	if e.BreakGlass.AuthorizingConfigModified {
		t.Fatal("no authorizing path changed; flag must be false")
	}
	if e.Actor != "alice" || e.RunURL != "https://ci.example/run/7" || e.CommitSHA != bgSHA {
		t.Fatalf("audit context mismatch: %+v", e)
	}
}

func TestBreakGlassUnconfiguredFailsClosed(t *testing.T) {
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(nil), store)

	_, err := Apply(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want polite not-configured error, got %v", err)
	}
	if len(engine.applied) != 0 {
		t.Fatal("nothing may be applied when break-glass is unconfigured")
	}
}

func TestBreakGlassDeniedActorFailsClosedWithTrace(t *testing.T) {
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"bob"}},
	}), store)

	_, err := Apply(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "denied") || !strings.Contains(err.Error(), "internal_list") {
		t.Fatalf("want denial with trace, got %v", err)
	}
	if len(engine.applied) != 0 {
		t.Fatal("nothing may be applied on denial")
	}
}

func TestBreakGlassEmptyJustificationRejected(t *testing.T) {
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{Anyone: true},
	}), store)
	in.BreakGlass = &BreakGlassRequest{Justification: "   "}

	_, err := Apply(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "justification") {
		t.Fatalf("want justification error, got %v", err)
	}
}

func TestBreakGlassNeverBypassesLocks(t *testing.T) {
	ctx := context.Background()
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
	}), store)

	// Another PR holds the lock.
	if _, acquired, err := in.Locks.TryAcquire(ctx, "api", "prod", corelocks.Holder{PR: 999, RunID: "other"}, time.Hour); err != nil || !acquired {
		t.Fatalf("seed lock: acquired=%v err=%v", acquired, err)
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatal("break-glass must NEVER bypass locks")
	}
	if len(engine.applied) != 0 {
		t.Fatal("engine.Apply must not run while the lock is held")
	}
	e := readAuditEntry(t, store)
	if e.Outcome != "blocked" {
		t.Fatalf("audit outcome = %q, want blocked", e.Outcome)
	}
}

func TestBreakGlassFreezeOverride(t *testing.T) {
	frozen := func(bg *schemas.BreakGlassYAML) *schemas.Shared {
		s := bgShared(bg)
		// Fires hourly, lasts two hours: always active.
		s.FreezeWindows = []schemas.FreezeWindowYAML{{Name: "always", Cron: "0 * * * *", Duration: "2h"}}
		return s
	}

	t.Run("default overrides freeze", func(t *testing.T) {
		engine, fv := newBGFixture()
		store, _ := filesystem.New(t.TempDir())
		in := bgApplyInput(t, engine, fv, frozen(&schemas.BreakGlassYAML{
			Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
		}), store)
		out, err := Apply(context.Background(), in)
		if err != nil || out.Blocked {
			t.Fatalf("override_freeze default(true) must apply through freeze: err=%v blocked=%v", err, out != nil && out.Blocked)
		}
		e := readAuditEntry(t, store)
		got := strings.Join(e.BreakGlass.OverriddenGates, ",")
		if !strings.Contains(got, "approvals") || !strings.Contains(got, "not_in_freeze") {
			t.Fatalf("overridden gates = %q, want approvals + not_in_freeze", got)
		}
	})

	t.Run("override_freeze false keeps freeze binding", func(t *testing.T) {
		engine, fv := newBGFixture()
		store, _ := filesystem.New(t.TempDir())
		off := false
		in := bgApplyInput(t, engine, fv, frozen(&schemas.BreakGlassYAML{
			Authorized:     schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
			OverrideFreeze: &off,
		}), store)
		out, err := Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Blocked {
			t.Fatal("override_freeze=false must stay blocked by freeze")
		}
		if len(engine.applied) != 0 {
			t.Fatal("engine.Apply must not run in freeze")
		}
	})
}

func TestBreakGlassSamePRConfigModificationFlagged(t *testing.T) {
	engine, fv := newBGFixture()
	fv.changed = append(fv.changed, ".reeve/shared.yaml", ".github/CODEOWNERS")
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{Anyone: true},
	}), store)

	out, err := Apply(context.Background(), in)
	if err != nil || out.Blocked {
		t.Fatalf("Apply: err=%v", err)
	}
	e := readAuditEntry(t, store)
	if e.BreakGlass == nil || !e.BreakGlass.AuthorizingConfigModified {
		t.Fatalf("same-PR modification must be flagged: %+v", e.BreakGlass)
	}
	paths := strings.Join(e.BreakGlass.AuthorizingPathsModified, ",")
	if !strings.Contains(paths, ".reeve/shared.yaml") || !strings.Contains(paths, ".github/CODEOWNERS") {
		t.Fatalf("modified paths mismatch: %q", paths)
	}
	if !strings.Contains(fv.allComments(), "modified in this same PR") {
		t.Fatal("PR comment must surface the same-PR modification flag")
	}
}

func TestBreakGlassCodeownersSource(t *testing.T) {
	engine, fv := newBGFixture()
	fv.codeowners = "projects/api/ @alice\n"
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{Codeowners: true},
	}), store)

	out, err := Apply(context.Background(), in)
	if err != nil || out.Blocked {
		t.Fatalf("codeowner actor must be authorized: err=%v", err)
	}
	e := readAuditEntry(t, store)
	if e.BreakGlass.AuthorizedVia != "codeowners" {
		t.Fatalf("authorized_via = %q, want codeowners", e.BreakGlass.AuthorizedVia)
	}
}

func TestBreakGlassVCSBypassNotYetSupported(t *testing.T) {
	engine, fv := newBGFixture()
	store, _ := filesystem.New(t.TempDir())
	in := bgApplyInput(t, engine, fv, bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{VCSBypass: true},
	}), store)

	_, err := Apply(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("want not-yet-supported error, got %v", err)
	}
	if len(engine.applied) != 0 {
		t.Fatal("nothing may be applied for an unsupported source")
	}
}
