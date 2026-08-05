package preconditions

import (
	"testing"
	"time"
)

func TestForkDeniedByDefault(t *testing.T) {
	res := Evaluate(Config{}, Inputs{PRIsFork: true})
	if !res.Blocked {
		t.Fatal("fork PR should be blocked by default")
	}
	if res.Gates[0].Gate != GateFork || res.Gates[0].Outcome != OutcomeFail {
		t.Fatalf("first gate should be fork fail: %+v", res.Gates[0])
	}
}

func TestForkWithOptInPasses(t *testing.T) {
	res := Evaluate(Config{RequireUpToDate: true}, Inputs{
		StackRef: "api/prod", PRIsFork: true, ForkOptInAllowed: true,
		UpToDate: true, ChecksGreen: true, PreviewSucceeded: true,
		ApprovalsSatisfied: true, LockAcquirable: true,
	})
	if res.Blocked {
		t.Fatalf("fork with opt-in + all gates green should pass: %+v", res)
	}
}

func TestFailFastStopsTrace(t *testing.T) {
	// Preview failed → stop before approvals/lock.
	res := Evaluate(Config{}, Inputs{PreviewSucceeded: false})
	if !res.Blocked {
		t.Fatal("expected blocked")
	}
	// GateOrder: fork(skip), up_to_date(skip), checks_green(skip), preview_succeeded(fail)
	last := res.Gates[len(res.Gates)-1]
	if last.Gate != GatePreviewOK || last.Outcome != OutcomeFail {
		t.Fatalf("expected preview_succeeded fail last, got %+v", last)
	}
}

func TestPreviewFreshness(t *testing.T) {
	cfg := Config{PreviewFreshness: time.Hour}
	old := Inputs{
		PreviewSucceeded: true, HasFreshPreview: true, PreviewAge: 2 * time.Hour,
		ApprovalsSatisfied: true, LockAcquirable: true,
	}
	res := Evaluate(cfg, old)
	if !res.Blocked {
		t.Fatal("stale preview should block")
	}
	found := false
	for _, g := range res.Gates {
		if g.Gate == GatePreviewFresh && g.Outcome == OutcomeFail {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected preview_fresh fail: %+v", res.Gates)
	}
}

func TestLockBlockedSurfacesHolder(t *testing.T) {
	res := Evaluate(Config{}, Inputs{
		PreviewSucceeded: true, ApprovalsSatisfied: true,
		LockAcquirable: false, LockBlockedByPR: 482,
	})
	if !res.Blocked {
		t.Fatal("expected blocked")
	}
	last := res.Gates[len(res.Gates)-1]
	if last.Gate != GateLock {
		t.Fatalf("expected last gate to be lock, got %v", last.Gate)
	}
	if want := "#482"; !contains(last.Reason, want) {
		t.Fatalf("reason should mention #482: %q", last.Reason)
	}
}

func TestFreezeBlocks(t *testing.T) {
	res := Evaluate(Config{}, Inputs{
		PreviewSucceeded: true, ApprovalsSatisfied: true, LockAcquirable: true,
		InFreeze: true, FreezeName: "friday-afternoon",
	})
	if !res.Blocked {
		t.Fatal("freeze should block")
	}
}

func TestDraftPRBlocked(t *testing.T) {
	res := Evaluate(Config{}, Inputs{PRIsDraft: true, PreviewSucceeded: true, ApprovalsSatisfied: true, LockAcquirable: true})
	if !res.Blocked {
		t.Fatal("draft PR should be blocked")
	}
	found := false
	for _, g := range res.Gates {
		if g.Gate == GateDraft && g.Outcome == OutcomeFail {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected not_draft_pr fail gate: %+v", res.Gates)
	}
}

func TestNonDraftPRSkipsDraftGate(t *testing.T) {
	res := Evaluate(Config{}, Inputs{PRIsDraft: false, PreviewSucceeded: true, ApprovalsSatisfied: true, LockAcquirable: true})
	for _, g := range res.Gates {
		if g.Gate == GateDraft && g.Outcome == OutcomeFail {
			t.Fatalf("non-draft PR should not fail draft gate: %+v", g)
		}
	}
}

func TestHappyPath(t *testing.T) {
	cfg := Config{RequireUpToDate: true, RequireChecksPassing: true, PreviewFreshness: time.Hour}
	in := Inputs{
		StackRef: "api/prod", UpToDate: true, ChecksGreen: true,
		PreviewSucceeded: true, HasFreshPreview: true, PreviewAge: 10 * time.Minute,
		ApprovalsSatisfied: true, LockAcquirable: true,
	}
	res := Evaluate(cfg, in)
	if res.Blocked {
		t.Fatalf("happy path should not block: %+v", res)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- break-glass gate composition ---

func gateFor(res Result, id GateID) (GateResult, bool) {
	for _, g := range res.Gates {
		if g.Gate == id {
			return g, true
		}
	}
	return GateResult{}, false
}

func overrode(res Result, id GateID) bool {
	for _, g := range res.Overridden {
		if g == id {
			return true
		}
	}
	return false
}

func TestBreakGlassOverridesApprovals(t *testing.T) {
	in := Inputs{
		PreviewSucceeded: true, LockAcquirable: true,
		ApprovalsSatisfied: false,
		BreakGlass:         true, BreakGlassOverrideFreeze: true,
	}
	res := Evaluate(Config{}, in)
	if res.Blocked {
		t.Fatalf("break-glass must override the approvals gate: %+v", res.Gates)
	}
	g, _ := gateFor(res, GateApprovals)
	if g.Outcome != OutcomeWarning || !contains(g.Reason, "break-glass") {
		t.Fatalf("approvals gate should warn as overridden: %+v", g)
	}
	if !overrode(res, GateApprovals) {
		t.Fatalf("approvals must be recorded as overridden: %+v", res.Overridden)
	}
}

func TestBreakGlassSatisfiedApprovalsNotOverridden(t *testing.T) {
	in := Inputs{
		PreviewSucceeded: true, LockAcquirable: true,
		ApprovalsSatisfied: true,
		BreakGlass:         true, BreakGlassOverrideFreeze: true,
	}
	res := Evaluate(Config{}, in)
	if overrode(res, GateApprovals) {
		t.Fatal("satisfied approvals must not be recorded as overridden")
	}
}

func TestBreakGlassFreezeOverrideConditional(t *testing.T) {
	base := Inputs{
		PreviewSucceeded: true, LockAcquirable: true, ApprovalsSatisfied: false,
		InFreeze: true, FreezeName: "holiday",
		BreakGlass: true,
	}

	on := base
	on.BreakGlassOverrideFreeze = true
	res := Evaluate(Config{}, on)
	if res.Blocked {
		t.Fatalf("override_freeze=true must override the freeze gate: %+v", res.Gates)
	}
	g, _ := gateFor(res, GateFreeze)
	if g.Outcome != OutcomeWarning || !contains(g.Reason, "holiday") {
		t.Fatalf("freeze gate should warn with the window name: %+v", g)
	}
	if !overrode(res, GateFreeze) {
		t.Fatalf("freeze must be recorded as overridden: %+v", res.Overridden)
	}

	off := base
	off.BreakGlassOverrideFreeze = false
	res = Evaluate(Config{}, off)
	if !res.Blocked {
		t.Fatal("override_freeze=false must keep the freeze gate binding")
	}
	g, _ = gateFor(res, GateFreeze)
	if g.Outcome != OutcomeFail {
		t.Fatalf("freeze gate should fail: %+v", g)
	}
	if overrode(res, GateFreeze) {
		t.Fatal("blocked freeze must not be recorded as overridden")
	}
}

func TestBreakGlassNeverOverridesLocks(t *testing.T) {
	in := Inputs{
		PreviewSucceeded: true, ApprovalsSatisfied: false,
		LockAcquirable: false, LockBlockedByPR: 7,
		BreakGlass: true, BreakGlassOverrideFreeze: true,
	}
	res := Evaluate(Config{}, in)
	if !res.Blocked {
		t.Fatal("break-glass must NEVER bypass locks")
	}
	g, _ := gateFor(res, GateLock)
	if g.Outcome != OutcomeFail || !contains(g.Reason, "#7") {
		t.Fatalf("lock gate should fail with holder PR: %+v", g)
	}
}

func TestBreakGlassStillEnforcesChecksAndPreview(t *testing.T) {
	cfg := Config{RequireChecksPassing: true}
	in := Inputs{
		ChecksGreen: false, PreviewSucceeded: true,
		ApprovalsSatisfied: false, LockAcquirable: true,
		BreakGlass: true, BreakGlassOverrideFreeze: true,
	}
	res := Evaluate(cfg, in)
	if !res.Blocked {
		t.Fatal("break-glass must not bypass checks_green")
	}
	g, _ := gateFor(res, GateChecksGreen)
	if g.Outcome != OutcomeFail {
		t.Fatalf("checks gate should fail: %+v", g)
	}

	in.ChecksGreen = true
	in.PreviewSucceeded = false
	res = Evaluate(cfg, in)
	if !res.Blocked {
		t.Fatal("break-glass must not bypass preview_succeeded")
	}
}

func TestBreakGlassStillEnforcesDraftAndFork(t *testing.T) {
	in := Inputs{
		PRIsDraft: true, PreviewSucceeded: true,
		ApprovalsSatisfied: false, LockAcquirable: true,
		BreakGlass: true, BreakGlassOverrideFreeze: true,
	}
	if res := Evaluate(Config{}, in); !res.Blocked {
		t.Fatal("break-glass must not bypass the draft gate")
	}
	in.PRIsDraft = false
	in.PRIsFork = true
	if res := Evaluate(Config{FailOnForkPRs: true}, in); !res.Blocked {
		t.Fatal("break-glass must not bypass the fork gate")
	}
}
