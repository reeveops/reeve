// Package preconditions evaluates apply gates in order. Pure function over
// plain data - run/apply.go populates Inputs, this package emits a Result
// the renderer can display. See openspec/specs/core/preconditions.
package preconditions

import (
	"fmt"
	"time"
)

// Outcome is the disposition of a single gate.
type Outcome string

const (
	OutcomePass    Outcome = "pass"
	OutcomeFail    Outcome = "fail"
	OutcomeWarning Outcome = "warn"
	OutcomeSkipped Outcome = "skipped"
)

// GateID enumerates every precondition.
type GateID string

const (
	GateUpToDate     GateID = "up_to_date"
	GateChecksGreen  GateID = "checks_green"
	GatePreviewFresh GateID = "preview_fresh"
	GatePreviewOK    GateID = "preview_succeeded"
	GatePolicy       GateID = "policy"
	GateApprovals    GateID = "approvals"
	GateLock         GateID = "lock_acquirable"
	GateFreeze       GateID = "not_in_freeze"
	GateFork         GateID = "fork_pr_policy"
	GateDraft        GateID = "not_draft_pr"
)

// GateOrder is the authoritative ordering (fail-fast semantics).
var GateOrder = []GateID{
	GateFork,  // fork-PR gate first - denies regardless of other state
	GateDraft, // draft PRs cannot be applied
	GateUpToDate,
	GateChecksGreen,
	GatePreviewOK,
	GatePreviewFresh,
	GatePolicy,
	GateApprovals,
	GateLock,
	GateFreeze,
}

// GateResult captures one gate's evaluation for one stack.
type GateResult struct {
	Gate    GateID
	Outcome Outcome
	Reason  string
}

// Config carries the user-configurable surface from shared.yaml.
type Config struct {
	RequireUpToDate         bool
	RequireChecksPassing    bool
	PreviewFreshness        time.Duration // 0 disables the gate
	PreviewMaxCommitsBehind int
	// FailOnForkPRs: if true, apply is denied on fork PRs with no opt-in.
	// Opt-in is a per-repo setting outside this package's scope - run/apply.go
	// passes the already-resolved answer via Inputs.ForkOptInAllowed.
	FailOnForkPRs bool
}

// Inputs bundles everything needed to evaluate all gates for one stack.
type Inputs struct {
	StackRef string // "project/stack"

	// PR state.
	PRIsFork         bool
	PRIsDraft        bool
	ForkOptInAllowed bool // per-repo opt-in for fork-PR apply
	UpToDate         bool
	CommitsBehind    int
	ChecksGreen      bool

	// Preview artifact state.
	HasFreshPreview  bool
	PreviewAge       time.Duration
	PreviewSucceeded bool

	// Policy hook aggregate result (set by policy module; Phase 6 wires it).
	// nil means "no hooks configured" → skip.
	PolicyPassed *bool

	// Approvals result from core/approvals.
	ApprovalsSatisfied bool

	// Lock acquirability.
	LockAcquirable  bool
	LockBlockedByPR int // if not acquirable, the holder PR number (0 if none)

	// Freeze windows: true if currently in a freeze that blocks this stack.
	InFreeze   bool
	FreezeName string

	// Break-glass emergency override (already authorized by
	// core/breakglass before it reaches here). It overrides the approvals
	// gate always, overrides freeze only when BreakGlassOverrideFreeze,
	// and NEVER touches any other gate - locks, checks, preview
	// freshness, policy, fork/draft all still apply. Break-glass replaces
	// human sign-off; it is not a license to apply stale/unchecked code.
	BreakGlass               bool
	BreakGlassOverrideFreeze bool
}

// Result aggregates gate outcomes for a single stack.
type Result struct {
	StackRef string
	Gates    []GateResult
	Blocked  bool // true if any gate failed with OutcomeFail
	// Overridden lists gates that WOULD have failed but were overridden
	// by break-glass. Feeds the audit record and the loud PR comment.
	Overridden []GateID
}

// Evaluate runs gates in GateOrder. Fail-fast stops the trace at the first
// Fail outcome - earlier passes still appear.
func Evaluate(cfg Config, in Inputs) Result {
	res := Result{StackRef: in.StackRef}
	for _, g := range GateOrder {
		gr, overridden := evalGate(g, cfg, in)
		res.Gates = append(res.Gates, gr)
		if overridden {
			res.Overridden = append(res.Overridden, g)
		}
		if gr.Outcome == OutcomeFail {
			res.Blocked = true
			break
		}
	}
	return res
}

// EvaluateAll runs every gate in GateOrder with no fail-fast stop, so the
// full trace is available even past the first failure. Report-only
// surfaces (/reeve explain) use this; the apply path keeps Evaluate so a
// blocked run does not spend VCS calls evaluating gates it will never
// reach.
func EvaluateAll(cfg Config, in Inputs) Result {
	res := Result{StackRef: in.StackRef}
	for _, g := range GateOrder {
		gr, overridden := evalGate(g, cfg, in)
		res.Gates = append(res.Gates, gr)
		if overridden {
			res.Overridden = append(res.Overridden, g)
		}
		if gr.Outcome == OutcomeFail {
			res.Blocked = true
		}
	}
	return res
}

// evalGate evaluates one gate. The second return is true when the gate
// would have failed but was overridden by break-glass (approvals always;
// freeze only with BreakGlassOverrideFreeze). Overridden gates come back
// as OutcomeWarning so they stay visible in the gate trace without
// blocking. Every other gate is delegated to the base logic untouched -
// break-glass NEVER overrides locks, checks, preview state, policy, or
// the fork/draft gates.
func evalGate(g GateID, cfg Config, in Inputs) (GateResult, bool) {
	if in.BreakGlass {
		switch g {
		case GateApprovals:
			if in.ApprovalsSatisfied {
				return pass(g, "approvals satisfied"), false
			}
			return warn(g, "approvals not satisfied - overridden by break-glass"), true
		case GateFreeze:
			if in.InFreeze && in.BreakGlassOverrideFreeze {
				return warn(g, fmt.Sprintf("in freeze window %q - overridden by break-glass", in.FreezeName)), true
			}
		}
	}
	return evalGateBase(g, cfg, in), false
}

func evalGateBase(g GateID, cfg Config, in Inputs) GateResult {
	switch g {
	case GateFork:
		if in.PRIsFork && !in.ForkOptInAllowed {
			return fail(g, "fork PR - apply denied by default; see docs/auth.md#fork-pr-policy to opt in")
		}
		if !in.PRIsFork {
			return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "not a fork PR"}
		}
		return pass(g, "fork PR with per-repo opt-in")

	case GateDraft:
		if in.PRIsDraft {
			return fail(g, "PR is in draft - convert to ready for review before applying")
		}
		return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "not a draft PR"}

	case GateUpToDate:
		if !cfg.RequireUpToDate {
			return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "require_up_to_date disabled"}
		}
		if in.UpToDate {
			return pass(g, "branch up-to-date with base")
		}
		msg := "branch is behind base"
		if cfg.PreviewMaxCommitsBehind > 0 && in.CommitsBehind <= cfg.PreviewMaxCommitsBehind {
			return warn(g, fmt.Sprintf("%d commit(s) behind (within max=%d)", in.CommitsBehind, cfg.PreviewMaxCommitsBehind))
		}
		return fail(g, msg)

	case GateChecksGreen:
		if !cfg.RequireChecksPassing {
			return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "require_checks_passing disabled"}
		}
		if in.ChecksGreen {
			return pass(g, "required checks passing")
		}
		return fail(g, "required checks are not passing")

	case GatePreviewOK:
		if in.PreviewSucceeded {
			return pass(g, "preview succeeded")
		}
		return fail(g, "preview failed for this stack")

	case GatePreviewFresh:
		if cfg.PreviewFreshness == 0 {
			// Deliberately disabled. Say what is no longer being checked
			// rather than just "disabled": a saved plan proves what THIS PR
			// intended, not that the world still matches it. Another PR can
			// have merged and changed a resource this plan touches, so an
			// old plan can apply cleanly and still be wrong, or fail
			// mid-apply on a conflict.
			return GateResult{Gate: g, Outcome: OutcomeSkipped,
				Reason: "preview_freshness disabled - a plan of any age may be applied; concurrent merges are not accounted for"}
		}
		if !in.HasFreshPreview {
			return fail(g, "no preview on current HEAD")
		}
		if in.PreviewAge > cfg.PreviewFreshness {
			return fail(g, fmt.Sprintf("preview age %s exceeds freshness window %s", in.PreviewAge, cfg.PreviewFreshness))
		}
		return pass(g, fmt.Sprintf("preview age %s within window %s", in.PreviewAge, cfg.PreviewFreshness))

	case GatePolicy:
		if in.PolicyPassed == nil {
			return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "no policy hooks configured"}
		}
		if *in.PolicyPassed {
			return pass(g, "all policy hooks passed")
		}
		return fail(g, "one or more policy hooks failed")

	case GateApprovals:
		if in.ApprovalsSatisfied {
			return pass(g, "approvals satisfied")
		}
		return fail(g, "approvals not satisfied")

	case GateLock:
		if in.LockAcquirable {
			return pass(g, "lock is acquirable")
		}
		if in.LockBlockedByPR > 0 {
			return fail(g, fmt.Sprintf("blocked by lock held by PR #%d", in.LockBlockedByPR))
		}
		return fail(g, "lock not acquirable")

	case GateFreeze:
		if in.InFreeze {
			return fail(g, fmt.Sprintf("in freeze window %q", in.FreezeName))
		}
		return pass(g, "not in freeze window")
	}
	return GateResult{Gate: g, Outcome: OutcomeSkipped, Reason: "unknown gate"}
}

func pass(g GateID, msg string) GateResult {
	return GateResult{Gate: g, Outcome: OutcomePass, Reason: msg}
}
func fail(g GateID, msg string) GateResult {
	return GateResult{Gate: g, Outcome: OutcomeFail, Reason: msg}
}
func warn(g GateID, msg string) GateResult {
	return GateResult{Gate: g, Outcome: OutcomeWarning, Reason: msg}
}
