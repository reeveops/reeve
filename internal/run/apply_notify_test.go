package run

import (
	"context"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
)

// applyNotifyFixture wires a single-stack apply with a timeline_github
// notification channel subscribed to the approved/applying events, so the
// announcements surface in the PR comment the fake VCS records.
func applyNotifyFixture(t *testing.T, approvalsGreen bool) (ApplyInput, *bgVCS) {
	t.Helper()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, blobStore)
	if !approvalsGreen {
		fv.approvalsList = nil // the approvals gate fails for every stack
	}
	in.Notifications = &schemas.Notifications{
		Header:   schemas.Header{Version: 2, ConfigType: "notifications"},
		Channels: []schemas.ChannelYAML{{Type: "timeline_github", On: []string{"approved", "applying"}}},
	}
	return in, fv
}

// TestGateBlockedApplySendsNoApprovedNotifications pins the ordering fix:
// approved/applying were announced before per-stack gates ran, so a run
// whose gates deny everything told the channel "approved" and "applying"
// while nothing applied. A gate-blocked run sends NEITHER - the gate
// comment is the only notice, and it already names the gate.
func TestGateBlockedApplySendsNoApprovedNotifications(t *testing.T) {
	ctx := context.Background()
	in, fv := applyNotifyFixture(t, false)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("gate-blocked run must be blocked: %+v", out)
	}

	comments := fv.allComments()
	for _, banned := range []string{"✅ **approved**", "🚀 **apply started**"} {
		if strings.Contains(comments, banned) {
			t.Fatalf("gate-blocked run must not announce %q:\n%s", banned, comments)
		}
	}
	if !strings.Contains(comments, "approvals") {
		t.Fatalf("gate-blocked run must still post the gate comment naming the gate:\n%s", comments)
	}
}

// TestApplyingAnnouncedOnlyAfterGatesPass is the control: a run that
// proceeds past gates announces approved + applying exactly once, on the
// PR comment timeline.
func TestApplyingAnnouncedOnlyAfterGatesPass(t *testing.T) {
	ctx := context.Background()
	in, fv := applyNotifyFixture(t, true)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Blocked || out.Failed {
		t.Fatalf("run must apply: blocked=%v failed=%v", out.Blocked, out.Failed)
	}

	comments := fv.allComments()
	if !strings.Contains(comments, "✅ **approved**") {
		t.Fatalf("approved announcement missing after gates pass:\n%s", comments)
	}
	if !strings.Contains(comments, "🚀 **apply started**") {
		t.Fatalf("applying announcement missing after gates pass:\n%s", comments)
	}
}

// TestBreakGlassAnnouncesAtAuthorization pins the break-glass ordering: the
// notification fires at AUTHORIZATION time - before any gate runs - and a
// final outcome of all-blocked never emits approved or applying.
func TestBreakGlassAnnouncesAtAuthorization(t *testing.T) {
	ctx := context.Background()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	noFreezeOverride := false
	shared := bgShared(&schemas.BreakGlassYAML{
		Authorized: schemas.BreakGlassAuthorized{InternalList: []string{"alice"}},
		// OverrideFreeze explicitly false: the freeze gate denies despite
		// the override, so EVERY stack is blocked while break-glass itself
		// is authorized.
		OverrideFreeze: &noFreezeOverride,
	})
	// A freeze window covering all stacks, active "now".
	shared.FreezeWindows = []schemas.FreezeWindowYAML{{
		Name: "quarterly", Cron: "* * * * *", Duration: "24h",
	}}
	in := bgApplyInput(t, engine, fv, shared, blobStore)
	in.Notifications = &schemas.Notifications{
		Header:   schemas.Header{Version: 2, ConfigType: "notifications"},
		Channels: []schemas.ChannelYAML{{Type: "timeline_github", On: []string{"break_glass", "approved", "applying"}}},
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("a freeze-blocked, break-glass run must still be blocked: %+v", out)
	}
	if len(engine.applied) != 0 {
		t.Fatalf("no stack may apply through the freeze gate: %v", engine.applied)
	}

	comments := fv.allComments()
	if !strings.Contains(comments, "break-glass override") {
		t.Fatalf("the break_glass notification must fire at authorization time:\n%s", comments)
	}
	for _, banned := range []string{"✅ **approved**", "🚀 **apply started**"} {
		if strings.Contains(comments, banned) {
			t.Fatalf("a fully blocked run must not announce %q:\n%s", banned, comments)
		}
	}
}

// TestApprovedAnnouncesOnlyPassedStacks pins the announcement contents: a
// run where one stack's gates pass and another's deny announces the passing
// stack only - never the blocked one.
func TestApprovedAnnouncesOnlyPassedStacks(t *testing.T) {
	ctx := context.Background()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &bgEngine{enum: []discovery.Stack{
		{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"},
		{Project: "worker", Path: "projects/worker", Name: "prod", Env: "prod"},
	}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts", "projects/worker/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, blobStore)
	// Declare BOTH stacks so the target covers the frozen one too.
	in.Config = &schemas.Engine{Engine: schemas.EngineBody{
		Type: "pulumi",
		Stacks: []schemas.StackDecl{
			{Project: "api", Path: "projects/api", Stacks: []string{"prod"}},
			{Project: "worker", Path: "projects/worker", Stacks: []string{"prod"}},
		},
	}}
	// worker/prod is frozen (the api stack is exempted by the glob); the
	// every-minute cron fires continuously, so the window is active.
	in.Shared.FreezeWindows = []schemas.FreezeWindowYAML{{
		Name: "quarterly", Cron: "* * * * *", Duration: "24h",
		Stacks: []string{"worker/prod"},
	}}
	// A two-stack manifest covers both stacks (a real preview writes one).
	if err := writeManifest(context.Background(), blobStore, 18, "preview-2", []summary.StackSummary{
		{Project: "api", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
		{Project: "worker", Stack: "prod", Env: "prod", Status: summary.StatusPlanned},
	}, bgSHA); err != nil {
		t.Fatal(err)
	}
	in.Notifications = &schemas.Notifications{
		Header:   schemas.Header{Version: 2, ConfigType: "notifications"},
		Channels: []schemas.ChannelYAML{{Type: "timeline_github", On: []string{"approved", "applying"}}},
	}

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("the frozen stack must block the run: %+v", out)
	}
	if len(engine.applied) != 1 || engine.applied[0] != "api/prod" {
		t.Fatalf("only the passing stack may apply: %v", engine.applied)
	}

	comments := fv.allComments()
	for _, want := range []string{"✅ **approved**", "🚀 **apply started**"} {
		if !strings.Contains(comments, want) {
			t.Fatalf("the passing stack must be announced (%s):\n%s", want, comments)
		}
	}
	// The announcement section must not name the blocked stack as applying.
	if !strings.Contains(comments, "worker/prod") {
		t.Fatalf("the blocked stack's gate section must be present:\n%s", comments)
	}
	approvedLine := ""
	for _, line := range strings.Split(comments, "\n") {
		if strings.Contains(line, "approved**") {
			approvedLine = line
			break
		}
	}
	if strings.Contains(approvedLine, "worker/prod") {
		t.Fatalf("the approved announcement must list only gate-passing stacks:\n%s", approvedLine)
	}
}
