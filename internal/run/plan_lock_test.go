package run

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
)

// lockEngine is a bgEngine that CAN save and execute plans, and records the
// PlanPath each apply was handed.
type lockEngine struct {
	bgEngine
	planPaths []string
	contents  []string
}

func (f *lockEngine) Capabilities() iac.Capabilities {
	return iac.Capabilities{SupportsSavedPlans: true, SupportsRefresh: true}
}

func (f *lockEngine) Apply(ctx context.Context, s discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	f.applied = append(f.applied, s.Ref())
	f.planPaths = append(f.planPaths, opts.PlanPath)
	if opts.PlanPath != "" {
		data, err := os.ReadFile(opts.PlanPath)
		if err != nil {
			return iac.ApplyResult{Error: "locked plan unreadable: " + err.Error()}, nil
		}
		f.contents = append(f.contents, string(data))
	}
	return iac.ApplyResult{Counts: summary.Counts{Add: 1}}, nil
}

// Stack refs come from the repo, so they can carry separators and traversal.
// The key must stay inside the run's own prefix regardless.
func TestPlanArtifactKeyCannotEscapeTheRunPrefix(t *testing.T) {
	for _, ref := range []string{"api/prod", "../../etc/passwd", "a/../../b", "weird name/../x"} {
		key := PlanArtifactKey(18, "run-1-abc", ref)
		if !strings.HasPrefix(key, "runs/pr-18/run-1-abc/plans/") {
			t.Errorf("ref %q produced key %q outside the run prefix", ref, key)
		}
		if strings.Contains(strings.TrimPrefix(key, "runs/pr-18/run-1-abc/plans/"), "/") {
			t.Errorf("ref %q produced a nested key %q", ref, key)
		}
		if strings.Contains(key, "..") {
			t.Errorf("ref %q produced a traversal key %q", ref, key)
		}
	}
}

// A local run (PR 0) keys under runs/local/, matching where its manifest goes.
func TestPlanArtifactKeyForLocalRuns(t *testing.T) {
	if got := PlanArtifactKey(0, "run-1-abc", "api/prod"); got != "runs/local/run-1-abc/plans/api_prod.plan" {
		t.Errorf("PlanArtifactKey(local) = %q", got)
	}
}

// Round trip: what a preview stored is byte-identical to what an apply gets
// back, and the fetched copy is removed by its cleanup.
func TestPlanArtifactRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())

	src := t.TempDir() + "/plan.tfplan"
	want := "\x00binary plan bytes\xff"
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	key, err := PutPlanArtifact(ctx, store, 18, "run-1-abc", "api/prod", src)
	if err != nil {
		t.Fatalf("PutPlanArtifact: %v", err)
	}
	if key != PlanArtifactKey(18, "run-1-abc", "api/prod") {
		t.Errorf("stored under %q", key)
	}

	path, cleanup, err := FetchPlanArtifact(ctx, store, key)
	if err != nil {
		t.Fatalf("FetchPlanArtifact: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("round trip corrupted the plan: %q != %q", got, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("fetched plan mode = %o, want 0600: a plan on a shared runner must not be world-readable", perm)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup must remove the fetched plan from the runner's disk")
	}
}

// An empty artifact is refused rather than stored: an apply locked to a
// zero-byte plan fails deep inside the engine instead of here.
func TestPutPlanArtifactRefusesEmpty(t *testing.T) {
	store, _ := filesystem.New(t.TempDir())
	src := t.TempDir() + "/empty.tfplan"
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PutPlanArtifact(context.Background(), store, 18, "run-1", "api/prod", src); err == nil {
		t.Error("an empty plan artifact must not be stored")
	}
}

// The end-to-end promise: what the preview saved is the file the engine is
// handed at apply time.
func TestApplyExecutesTheSavedPlan(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	engine := &lockEngine{bgEngine: bgEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
	}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)

	// Stand in for the preview: store a plan artifact and a manifest that
	// points at it, exactly as run.Preview does.
	src := t.TempDir() + "/plan.tfplan"
	if err := os.WriteFile(src, []byte("the-reviewed-plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := PutPlanArtifact(ctx, store, 18, "preview-locked", "api/prod", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(ctx, store, 18, "preview-locked", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned, PlanKey: key},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(ctx, in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.planPaths) != 1 || engine.planPaths[0] == "" {
		t.Fatalf("apply was not handed a locked plan: %v", engine.planPaths)
	}
	if len(engine.contents) != 1 || engine.contents[0] != "the-reviewed-plan" {
		t.Fatalf("apply executed the wrong plan: %v", engine.contents)
	}
	// The runner-local copy does not outlive the run.
	if _, err := os.Stat(engine.planPaths[0]); !os.IsNotExist(err) {
		t.Error("the fetched plan must be removed once the engine is done with it")
	}
}

// A manifest with no plan artifact (written before plan locking, or by a
// run with locking off) still applies - and says so, because "this apply
// re-planned" must never be something an operator has to infer.
func TestApplyWithoutASavedPlanStillAppliesAndSaysSo(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	engine := &lockEngine{bgEngine: bgEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
	}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)

	if _, err := Apply(ctx, in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(engine.applied) != 1 {
		t.Fatalf("the stack must still apply, got %v", engine.applied)
	}
	if engine.planPaths[0] != "" {
		t.Errorf("no artifact exists, so PlanPath must be empty, got %q", engine.planPaths[0])
	}
	if body := fv.allComments(); !strings.Contains(body, "plan lock unavailable") {
		t.Errorf("the degraded run must be visible on the PR:\n%s", body)
	}
}

// --refresh and plan locking cannot both hold. The run turns locking off
// rather than handing the engine a combination it must reject.
func TestApplyWithRefreshDropsPlanLocking(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	engine := &lockEngine{bgEngine: bgEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
	}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, store)
	in.Refresh = true

	src := t.TempDir() + "/plan.tfplan"
	if err := os.WriteFile(src, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := PutPlanArtifact(ctx, store, 18, "preview-locked", "api/prod", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(ctx, store, 18, "preview-locked", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned, PlanKey: key},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(ctx, in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if engine.planPaths[0] != "" {
		t.Errorf("--refresh must drop the locked plan, got %q", engine.planPaths[0])
	}
	if body := fv.allComments(); !strings.Contains(body, "refresh requested") {
		t.Errorf("a refresh-driven apply must be visible on the PR:\n%s", body)
	}
}

// savingEngine is a preview engine that writes a plan wherever it is asked
// to, the way a real CLI's `-out` / `--save-plan` does.
type savingEngine struct {
	enum      []discovery.Stack
	body      string
	savedTo   []string
	supported bool
}

func (f *savingEngine) Name() string { return "saving-fake" }
func (f *savingEngine) Capabilities() iac.Capabilities {
	return iac.Capabilities{SupportsSavedPlans: f.supported}
}
func (f *savingEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return f.enum, nil
}
func (f *savingEngine) Preview(_ context.Context, _ discovery.Stack, opts iac.PreviewOpts) (iac.PreviewResult, error) {
	res := iac.PreviewResult{Counts: summary.Counts{Add: 1}, PlanSummary: "+ one"}
	if opts.SavePlanPath == "" || !f.supported {
		return res, nil
	}
	if err := os.WriteFile(opts.SavePlanPath, []byte(f.body), 0o600); err != nil {
		return iac.PreviewResult{Error: err.Error()}, nil
	}
	f.savedTo = append(f.savedTo, opts.SavePlanPath)
	res.PlanPath = opts.SavePlanPath
	return res, nil
}

func previewInputFor(engine Engine, store blob.Store, fv *fakeVCS) PreviewInput {
	return PreviewInput{
		PRNumber:  42,
		CommitSHA: "abc12345xyz",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type:   "terraform",
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared:   &schemas.Shared{},
		Blob:     store,
		VCS:      fv,
		Comments: fv,
	}
}

// Preview stores the engine's plan and records its key, so apply has
// something to lock onto. It also cleans up the runner-local copy.
func TestPreviewStoresTheSavedPlan(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	engine := &savingEngine{
		enum:      []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
		body:      "the-plan-bytes",
		supported: true,
	}
	fv := &fakeVCS{changed: []string{"projects/api/main.tf"}}

	out, err := Preview(ctx, previewInputFor(engine, store, fv))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(out.Stacks) != 1 {
		t.Fatalf("want one stack, got %+v", out.Stacks)
	}
	key := out.Stacks[0].PlanKey
	if key == "" {
		t.Fatal("preview must record the stored plan's key; without it apply has nothing to lock onto")
	}
	path, cleanup, err := FetchPlanArtifact(ctx, store, key)
	if err != nil {
		t.Fatalf("stored plan is not retrievable: %v", err)
	}
	defer cleanup()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the-plan-bytes" {
		t.Errorf("stored plan = %q, want the engine's bytes", got)
	}
	// The temp the engine wrote into does not survive the preview.
	if len(engine.savedTo) != 1 {
		t.Fatalf("engine was asked to save %d times, want 1", len(engine.savedTo))
	}
	if _, err := os.Stat(engine.savedTo[0]); !os.IsNotExist(err) {
		t.Error("the preview-local plan file must be removed once it is uploaded")
	}
}

// An engine that cannot save plans is never asked to, and no key is
// recorded - apply then re-plans, which is the pre-locking behavior.
func TestPreviewSkipsPlanStorageWithoutTheCapability(t *testing.T) {
	store, _ := filesystem.New(t.TempDir())
	engine := &savingEngine{
		enum:      []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
		supported: false,
	}
	fv := &fakeVCS{changed: []string{"projects/api/main.tf"}}

	out, err := Preview(context.Background(), previewInputFor(engine, store, fv))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if out.Stacks[0].PlanKey != "" {
		t.Errorf("PlanKey = %q, want empty for an engine that cannot save plans", out.Stacks[0].PlanKey)
	}
}

// plan_locking: false is honored at preview time too - nothing is stored,
// so the sensitive artifact never lands in the bucket.
func TestPreviewStoresNothingWhenPlanLockingIsOff(t *testing.T) {
	ctx := context.Background()
	store, _ := filesystem.New(t.TempDir())
	engine := &savingEngine{
		enum:      []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}},
		body:      "plan",
		supported: true,
	}
	fv := &fakeVCS{changed: []string{"projects/api/main.tf"}}
	in := previewInputFor(engine, store, fv)
	off := false
	in.Config.Engine.PlanLocking = &off

	out, err := Preview(ctx, in)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if out.Stacks[0].PlanKey != "" {
		t.Errorf("PlanKey = %q, want empty when plan_locking is off", out.Stacks[0].PlanKey)
	}
	keys, err := store.List(ctx, "runs/")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.Contains(k, "/plans/") {
			t.Errorf("plan_locking is off but a plan artifact was stored at %s", k)
		}
	}
}

// Config intent: omitted means locked, and the opt-out is honored.
func TestPlanLockingDefaultsOn(t *testing.T) {
	if !PlanLockingEnabled(nil) {
		t.Error("a nil engine config must default to locked")
	}
	if !PlanLockingEnabled(&schemas.Engine{}) {
		t.Error("an omitted plan_locking must default to locked")
	}
	off := false
	if PlanLockingEnabled(&schemas.Engine{Engine: schemas.EngineBody{PlanLocking: &off}}) {
		t.Error("plan_locking: false must turn locking off")
	}
	on := true
	if !PlanLockingEnabled(&schemas.Engine{Engine: schemas.EngineBody{PlanLocking: &on}}) {
		t.Error("plan_locking: true must keep locking on")
	}
}

// Capability detection degrades safely: an engine that cannot execute a
// saved plan is never asked to.
func TestEngineSupportsSavedPlans(t *testing.T) {
	if EngineSupportsSavedPlans(&bgEngine{}) {
		t.Error("an engine reporting SupportsSavedPlans=false must not be treated as lockable")
	}
	if !EngineSupportsSavedPlans(&lockEngine{}) {
		t.Error("an engine reporting SupportsSavedPlans=true must be treated as lockable")
	}
	if EngineSupportsSavedPlans(struct{}{}) {
		t.Error("a type that exposes no Capabilities must not be treated as lockable")
	}
}
