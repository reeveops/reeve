package run

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/render"
	"github.com/reeveops/reeve/internal/core/summary"
)

func explainFixture(t *testing.T) (*bgEngine, *bgVCS, ExplainInput) {
	t.Helper()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatalf("filesystem store: %v", err)
	}
	engine := &bgEngine{enum: []discovery.Stack{
		{Project: "api", Name: "prod", Env: "prod", Path: "projects/api"},
	}}
	fv := &bgVCS{changed: []string{"projects/api/main.go"}, headSHA: bgSHA}
	// A stored preview manifest so preview gates have something to report.
	if err := writeManifest(context.Background(), store, 18, "preview-1", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	shared := &schemas.Shared{Bucket: schemas.BucketConfig{Type: "filesystem"}}
	return engine, fv, ExplainInput{
		PRNumber:  18,
		CommitSHA: bgSHA,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type:   "pulumi",
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		Shared: shared,
		Blob:   store,
		Locks:  blocks.New(store),
		VCS:    fv,
	}
}

func TestExplainPostsReportOnlyComment(t *testing.T) {
	engine, fv, in := explainFixture(t)
	out, err := Explain(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("explain must never apply; applied=%v", engine.applied)
	}
	if !out.Blocked {
		t.Fatal("no approvals given: expected blocked=true")
	}
	marker := render.ExplainMarker(shortSHA(bgSHA))
	bodies := fv.comments[marker]
	if len(bodies) != 1 {
		t.Fatalf("expected one explain comment upsert under %q, got %d", marker, len(bodies))
	}
	body := bodies[0]
	for _, want := range []string{"Report-only", "api/prod", "apply gates", "approvals"} {
		if !strings.Contains(body, want) {
			t.Fatalf("explain comment missing %q:\n%s", want, body)
		}
	}
}

func TestExplainLockHeldByOtherPR(t *testing.T) {
	_, fv, in := explainFixture(t)
	// Another PR holds the lock: the gate must fail and name the holder.
	if _, _, err := in.Locks.TryAcquire(context.Background(), "api", "prod",
		corelocks.Holder{PR: 99, RunID: "run-99", Actor: "mallory"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	out, err := Explain(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := fv.comments[render.ExplainMarker(shortSHA(bgSHA))][0]
	if !strings.Contains(body, "held by PR #99") {
		t.Fatalf("expected lock holder PR #99 in comment:\n%s", body)
	}
	if !out.Blocked {
		t.Fatal("held lock should block")
	}
}

func TestExplainUnknownStackPostsError(t *testing.T) {
	_, fv, in := explainFixture(t)
	in.StackFilter = "nope/prod"
	_, err := Explain(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for unknown stack")
	}
	if !strings.Contains(err.Error(), "api/prod") {
		t.Fatalf("error must list valid refs, got: %v", err)
	}
	body := fv.allComments()
	if !strings.Contains(body, "unknown stack") || !strings.Contains(body, "api/prod") {
		t.Fatalf("error comment must name valid refs:\n%s", body)
	}
}

func TestExplainValidStackFilter(t *testing.T) {
	_, fv, in := explainFixture(t)
	in.StackFilter = "api/prod"
	out, err := Explain(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Body, "api/prod") {
		t.Fatalf("filtered report must cover api/prod:\n%s", out.Body)
	}
	if strings.Contains(out.Body, "unknown stack") {
		t.Fatalf("valid filter must not error:\n%s", out.Body)
	}
	if got := strings.Count(out.Body, "#### "); got != 1 {
		t.Fatalf("expected exactly one stack section, got %d:\n%s", got, out.Body)
	}
	if len(fv.comments[render.ExplainMarker(shortSHA(bgSHA))]) != 1 {
		t.Fatal("expected one posted comment")
	}
}

func TestExplainNilLockStore(t *testing.T) {
	_, fv, in := explainFixture(t)
	in.Locks = nil
	out, err := Explain(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := fv.comments[render.ExplainMarker(shortSHA(bgSHA))][0]
	if !strings.Contains(body, "unknown (no lock store)") {
		t.Fatalf("nil lock store must render as unknown:\n%s", body)
	}
	// Lock gate defaults to acquirable; blocked (if any) comes from other gates.
	if !strings.Contains(out.Body, "lock_acquirable") {
		t.Fatalf("gate trace must still include the lock gate:\n%s", out.Body)
	}
}

func TestExplainForkPRIdenticalPath(t *testing.T) {
	// Fork PRs run explain identically: no credentials involved, gate
	// trace still renders (with the fork gate failing closed by default).
	_, fv, in := explainFixture(t)
	fvPR := *fv
	fvPR.forkPR = true
	in.VCS = &fvPR
	out, err := Explain(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := fvPR.comments[render.ExplainMarker(shortSHA(bgSHA))][0]
	if !strings.Contains(body, "fork") {
		t.Fatalf("fork gate should appear in trace:\n%s", body)
	}
	if !out.Blocked {
		t.Fatal("fork PR without opt-in should show as blocked")
	}
}
