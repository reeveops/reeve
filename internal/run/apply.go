package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/audit"
	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/core/breakglass"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/preconditions"
	"github.com/reeveops/reeve/internal/core/render"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/notify"
	"github.com/reeveops/reeve/internal/observability/annotations"
	reeveotel "github.com/reeveops/reeve/internal/observability/otel"
	"github.com/reeveops/reeve/internal/vcs"
	"github.com/reeveops/reeve/internal/vcs/codeowners"
)

// applyEngine is what run/apply.go needs from an IaC adapter.
type applyEngine interface {
	Engine
	iac.Applier
}

// applyVCS is the extended VCS surface for apply-time gate resolution.
type applyVCS interface {
	prReader
	commentPoster
	Capabilities() vcs.CommentCapabilities
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
	// checks / up-to-date
	ChecksGreen(ctx context.Context, sha string, opts vcs.ChecksGreenOpts) (bool, []string, error)
	CompareBranches(ctx context.Context, base, head string) (int, error)
	// approvals
	approvals.Source
	// pr_comment approval source (opt-in via approvals.sources). Reads
	// historical PR comments and re-enforces the author_association gate.
	ListCommentApprovals(ctx context.Context, pr approvals.PR, cfg vcs.CommentApprovalConfig) ([]approvals.Approval, error)
	// CODEOWNERS
	FetchCodeowners(ctx context.Context) (string, error)
	// team expansion (called by core/approvals to resolve org/team rules)
	ListTeamMembers(ctx context.Context, slug string) ([]string, error)
}

// ApplyInput wires dependencies and run context.
type ApplyInput struct {
	PRNumber  int
	CommitSHA string // best-effort; overridden from PR HEAD post-GetPR
	RunNumber int
	CIRunID   int64
	CIRunURL  string
	// SelfCheckNames is the list of check_run names that belong to reeve
	// itself and must be skipped when computing ChecksGreen (otherwise a
	// previously failed apply pins the gate red on the same SHA forever).
	// Typically populated from $GITHUB_WORKFLOW + $GITHUB_JOB.
	SelfCheckNames []string
	RepoRoot       string
	RepoFull       string // "owner/name" for audit log
	Actor          string
	Engine         applyEngine
	Config         *schemas.Engine
	Shared         *schemas.Shared
	AuthConfig     *schemas.Auth
	AuthRegistry   *auth.Registry
	Notifications  *schemas.Notifications
	Blob           blob.Store
	Locks          *blocks.Store
	VCS            applyVCS
	AuditWriter    *audit.Writer
	OTEL           *reeveotel.Provider
	Annotations    []annotations.Emitter
	// TriggerSource records how this apply was initiated: "comment" (a
	// /reeve apply comment) or "merge" (the PR was merged). It is compared
	// against the configured apply.trigger mode so that exactly one
	// initiation path applies; a mismatch is a deliberate no-op. Empty is
	// treated as "comment" for backward compatibility. Break-glass runs are
	// exempt (see below).
	TriggerSource string
	// Force re-applies even when this commit is already recorded as applied,
	// bypassing the already-applied guard.
	Force bool
	// Refresh reconciles engine state with live infrastructure before
	// applying (`/reeve apply --refresh`). It is incompatible with plan
	// locking by construction - a refresh can change the diff, which is the
	// one thing a locked plan forbids - so requesting it turns locking off
	// for this run and says so on the timeline.
	Refresh bool
	// CommentApproval configures the opt-in pr_comment approval source
	// (approvals.sources). It carries the command prefixes and
	// author_association allowlist from the action inputs so the source can
	// re-enforce the same authorization gate action.yml uses for command
	// dispatch. Only consulted when approvals.sources enables pr_comment.
	CommentApproval vcs.CommentApprovalConfig
	// BreakGlass, when non-nil, requests an emergency apply: approvals are
	// overridden (freeze too, unless disabled in config), authorization is
	// resolved against the PR HEAD's break_glass config, and the run is
	// loudly audited. Locks and every other gate still apply.
	BreakGlass *BreakGlassRequest
}

// BreakGlassRequest carries the operator-supplied emergency context.
type BreakGlassRequest struct {
	// Justification is mandatory and non-empty; it lands verbatim in the
	// audit record and the PR comment.
	Justification string
}

// ApplyOutput bundles the artifacts from an apply run.
type ApplyOutput struct {
	Stacks      []summary.StackSummary
	CommentBody string
	RunID       string
	DurationSec int
	Blocked     bool // true if any stack was blocked by preconditions
	// Failed is true when at least one stack's apply errored (lock storage,
	// auth resolution, or engine failure). Callers MUST exit nonzero on it -
	// a failed apply is never a green run. Blocked without Failed is a
	// deliberate non-failure (preconditions held the run back; exit zero).
	Failed bool
	// FailedStacks lists the "project/stack" refs with StatusError, in run
	// order, for the caller's exit-error summary line.
	FailedStacks []string
}

// Apply runs apply for stacks affected by the PR. For each stack:
// 1. Acquire lock (or queue).
// 2. Evaluate preconditions.
// 3. If gates pass, run engine apply.
// 4. Release lock (promotes queue).
// 5. Emit audit entry.
// The PR comment is updated at the end with the aggregated results.
func Apply(ctx context.Context, in ApplyInput) (out *ApplyOutput, retErr error) {
	start := time.Now()
	runID := fmt.Sprintf("apply-%d-%s", in.RunNumber, shortSHA(in.CommitSHA))

	// Break-glass fail-fast: a missing justification never starts a run.
	if in.BreakGlass != nil && strings.TrimSpace(in.BreakGlass.Justification) == "" {
		return nil, errors.New("break-glass apply requires a non-empty justification")
	}

	// Trigger-mode enforcement (the binary is the source of truth for the
	// mode). apply.trigger selects exactly ONE initiation path: "comment"
	// (default) applies only from a /reeve apply comment; "merge" applies
	// only when the PR is merged. A request whose origin does not match the
	// configured mode is a deliberate no-op — never an error — so that a
	// mis-dispatched event (e.g. a merge event in a comment-mode repo, or a
	// comment in a merge-mode repo) can never force an apply. This is a flow
	// selector, not a gate: every downstream gate (approvals, checks,
	// preview freshness, locks, freeze) still applies unchanged on the path
	// that does match. Break-glass is an explicit, authorized emergency
	// override with its own strong authz + audit and is exempt from the
	// flow selector so the emergency lever works in either mode.
	if in.BreakGlass == nil {
		configuredMode := schemas.ApplyTriggerComment
		if in.Shared != nil {
			configuredMode = in.Shared.Apply.TriggerMode()
		}
		source := in.TriggerSource
		if source == "" {
			source = schemas.ApplyTriggerComment
		}
		if source != configuredMode {
			slog.Info("apply skipped: trigger source does not match configured apply.trigger mode",
				"trigger_source", source, "configured_mode", configuredMode, "pr", in.PRNumber, "sha", in.CommitSHA)
			return &ApplyOutput{RunID: runID, DurationSec: int(time.Since(start).Seconds())}, nil
		}
	}

	// OTEL root span for this run. Finished at return.
	ctx, endRun := in.OTEL.StartRunSpan(ctx, "apply", in.PRNumber, in.CommitSHA)
	// "outcome" is filled in along the way: explicit blocked/failed paths set
	// it directly; any error return that did not set a more specific outcome
	// is recorded as "error" so early failures never end the span "success".
	outcome := "success"
	defer func() {
		if retErr != nil && outcome == "success" {
			outcome = "error"
		}
		endRun(outcome)
	}()

	// Annotation: apply started.
	PostAnnotation(ctx, in.Annotations, annotations.EventApplyStarted,
		"", "", "", "", "", in.PRNumber, in.CommitSHA)

	if err := PulumiLogin(ctx, in.Config); err != nil {
		return nil, err
	}

	timeline := newApplyTimeline(in.VCS, in.Blob, in.PRNumber, runID, in.RunNumber, in.CommitSHA, in.CIRunURL)
	timeline.add(ctx, "🚀", "apply starting", "")

	// Already-applied guard: if this exact commit was fully applied before and
	// the caller didn't pass --force, there is nothing new to ship. Record it
	// on the timeline and exit success rather than re-running side effects.
	if !in.Force {
		if prior, _ := readAppliedState(ctx, in.Blob, in.PRNumber, in.CommitSHA); prior != nil {
			detail := fmt.Sprintf("commit %s was already applied on run #%d (%s). Comment `/reeve apply --force` to apply again.",
				shortSHA(in.CommitSHA), prior.RunNumber, prior.AppliedAt)
			timeline.add(ctx, "⏭️", "skipped", detail)
			slog.Info("apply skipped: already applied", "sha", in.CommitSHA, "prior_run", prior.RunNumber)
			return &ApplyOutput{RunID: runID, DurationSec: int(time.Since(start).Seconds())}, nil
		}
	}

	// 1. Resolve affected stacks (same pipeline as preview).
	enum, err := in.Engine.EnumerateStacks(ctx, in.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerate stacks: %w", err)
	}
	decls, filter := declarationsFromConfig(in.Config)
	declared := discovery.Resolve(enum, decls, filter)

	changed, err := in.VCS.ListChangedFiles(ctx, in.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	slog.Debug("changed files", "count", len(changed), "files", changed)
	cm := changeMappingFromConfig(in.Config)
	mapRes := discovery.AffectedDetailed(declared, changed, cm)
	target := mapRes.Stacks
	slog.Debug("apply target stacks", "count", len(target), "reason", mapRes.Reason)
	for _, s := range target {
		slog.Debug("target stack", "ref", s.Ref(), "path", s.Path)
	}

	// Docs/asset-only change: nothing to apply. Record on the timeline and exit.
	if mapRes.Reason == discovery.ReasonDocsOnly {
		timeline.add(ctx, "⏭️", "skipped", "documentation/asset-only changes — no Pulumi stacks affected")
		slog.Info("apply skipped: docs-only changes")
		return &ApplyOutput{RunID: runID, DurationSec: int(time.Since(start).Seconds())}, nil
	}
	// Bind the apply scope to what was previewed for THIS commit.
	//
	// Two things make a freshly-computed mapping unsafe here. First,
	// AffectedDetailed broadens to every stack the moment one changed file
	// maps to no stack, discarding the precise matches - fine for "show me
	// what might be affected", not for deciding what to apply. Second,
	// ListChangedFiles is a live diff against a moving base: if the base
	// advanced between preview and apply, the file list can differ from the
	// one the plan was built from, so the mapping can change under a commit
	// that never moved.
	//
	// The preview manifest is keyed by commit SHA and immutable, so it is
	// the only honest record of what was planned and approved. Apply
	// executes that set - never more.
	if previewed, ok := PreviewedStackRefs(ctx, in.Blob, in.PRNumber, in.CommitSHA); ok {
		bound := make([]discovery.Stack, 0, len(target))
		for _, s := range declared {
			if previewed[s.Ref()] {
				bound = append(bound, s)
			}
		}
		if dropped := scopeDelta(target, bound); len(dropped) > 0 {
			timeline.add(ctx, "🎯", "scope bound to plan", fmt.Sprintf(
				"apply targets the %d stack(s) previewed for %s; not applying %s, which this run's change mapping added but the plan did not cover",
				len(bound), shortSHA(in.CommitSHA), strings.Join(dropped, ", ")))
			slog.Warn("apply scope narrowed to the previewed set",
				"sha", in.CommitSHA, "previewed", len(bound), "mapped", len(target), "dropped", dropped)
		}
		target = bound
	} else if mapRes.Reason == discovery.ReasonBroadened {
		// No preview manifest for this commit: every stack will fail the
		// preview gate anyway, but do not silently widen the blast radius
		// on the way there. Report it and keep the precise matches.
		timeline.add(ctx, "📡", "scope broadened", fmt.Sprintf(
			"changed files map to no specific stack (%s) and no plan exists for %s; not widening the apply to every stack",
			strings.Join(mapRes.Unmapped, ", "), shortSHA(in.CommitSHA)))
		target = mapRes.Matched
	}

	if len(target) == 0 {
		timeline.add(ctx, "⏭️", "skipped", fmt.Sprintf(
			"no stack has a plan for %s; run a preview on this commit first", shortSHA(in.CommitSHA)))
		slog.Info("apply skipped: no previewed stacks for commit", "sha", in.CommitSHA)
		return &ApplyOutput{RunID: runID, DurationSec: int(time.Since(start).Seconds())}, nil
	}

	// 2. Per-stack context: PR + checks + upstream-commits + approvals + CODEOWNERS.
	pr, err := in.VCS.GetPR(ctx, in.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("get pr: %w", err)
	}
	slog.Debug("pr fetched", "number", in.PRNumber, "head_sha", pr.HeadSHA, "author", pr.Author, "base_ref", pr.BaseRef, "is_draft", pr.IsDraft, "is_fork", pr.IsFork)

	checksOpts := vcs.ChecksGreenOpts{
		IgnoreRunID: in.CIRunID,
		IgnoreNames: in.SelfCheckNames,
	}
	checksGreen, failingChecks, err := in.VCS.ChecksGreen(ctx, in.CommitSHA, checksOpts)
	if err != nil {
		// Fail-closed: an API outage must not silently pass the gate. The
		// gate-evaluation error is propagated so the caller can distinguish
		// "checks failed" from "checks could not be evaluated".
		return nil, fmt.Errorf("evaluate checks_green: %w", err)
	}
	if !checksGreen {
		slog.Info("required checks not green", "failing", failingChecks, "sha", in.CommitSHA)
	}

	behind, err := in.VCS.CompareBranches(ctx, pr.BaseRef, in.CommitSHA)
	if err != nil {
		// Fail-closed: previously this defaulted to behind=0 which made the
		// up-to-date gate silently pass on VCS outage.
		return nil, fmt.Errorf("compare branches %s..%s: %w", pr.BaseRef, in.CommitSHA, err)
	}
	upToDate := behind == 0

	// Use pr.HeadSHA (from the VCS API) for approval matching so that
	// dismiss_on_new_commit compares against the actual PR HEAD, not the
	// SHA the CI runner happened to check out (which may be a merge commit).
	approvalHeadSHA := pr.HeadSHA
	if approvalHeadSHA == "" {
		approvalHeadSHA = in.CommitSHA
	}
	// Approvals config drives which sources count. Extract it before gathering
	// so we can gather from exactly the enabled sources and union them.
	appCfg := toApprovalsConfig(in.Shared)
	approvalPR := approvals.PR{
		Number: in.PRNumber, HeadSHA: approvalHeadSHA, Author: pr.Author, Changed: changed,
	}

	// Gather approvals from every enabled source and union them. pr_review is
	// on by default; pr_comment is opt-in. Deduplication by approver identity
	// is handled downstream in Evaluate (which counts each login once), so a
	// human who approves via both a review AND a comment counts a single time.
	// When no sources block is configured, only pr_review runs - identical to
	// prior behavior.
	var reviewApprovals, commentApprovals []approvals.Approval
	if appCfg.PRReviewEnabled() {
		reviewApprovals, err = in.VCS.ListApprovals(ctx, approvalPR)
		if err != nil {
			// Fail-closed: previously the error was swallowed and rawApprovals
			// was nil, which made the approvals gate fail with "no approvals"
			// instead of surfacing the underlying VCS error.
			return nil, fmt.Errorf("list approvals: %w", err)
		}
	}
	if appCfg.PRCommentEnabled() {
		commentCfg := in.CommentApproval
		if commentCfg.Command == "" {
			commentCfg.Command = appCfg.CommentCommand()
		}
		commentApprovals, err = in.VCS.ListCommentApprovals(ctx, approvalPR, commentCfg)
		if err != nil {
			// Fail-closed, same as pr_review: a source error blocks rather than
			// silently dropping approvals.
			return nil, fmt.Errorf("list comment approvals: %w", err)
		}
		slog.Debug("comment approvals fetched", "count", len(commentApprovals))
	}
	rawApprovals := approvals.MergeApprovals(reviewApprovals, commentApprovals)
	slog.Debug("raw approvals fetched", "count", len(rawApprovals), "pr_head_sha", in.CommitSHA)
	for _, a := range rawApprovals {
		slog.Debug("raw approval", "approver", a.Approver, "commit_sha", a.CommitSHA, "source", a.Source)
	}

	// CODEOWNERS (optional). A 404 returns "" with nil error; only a real
	// transport error reaches here, and that must not silently pass the
	// codeowners gate.
	coContent, err := in.VCS.FetchCodeowners(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch codeowners: %w", err)
	}
	var coResolved map[string][]string
	if coContent != "" {
		rules := codeowners.Parse(strings.NewReader(coContent))
		coResolved = codeowners.Resolve(rules, changed)
	}

	// Freeze config.
	freezeCfg := toFreezeConfig(in.Shared)
	preCfg := toPreconditionsConfig(in.Shared)
	ttl := LockTTL(in.Shared)

	// Pre-resolve team-slug membership once for every stack we're about to
	// process. Without this, a rule like `approvers: [my-org/sre]` would
	// only match if the literal string "my-org/sre" appeared in the
	// approvals - i.e. never. Expansion is per-org, so resolving once
	// outside the loop is correct and avoids hammering the VCS API.
	stackRules := make([]approvals.Rules, 0, len(target))
	for _, s := range target {
		stackRules = append(stackRules, approvals.Resolve(appCfg, s.Ref()))
	}
	// Break-glass internal_list may name "org/team" slugs; feed them into
	// the same expansion pass so membership checks can resolve.
	bgCfg := toBreakGlassConfig(in.Shared)
	if in.BreakGlass != nil && len(bgCfg.InternalList) > 0 {
		stackRules = append(stackRules, approvals.Rules{Approvers: bgCfg.InternalList})
	}
	// Also expand any teams referenced in CODEOWNERS so matchesOne can
	// resolve them. Without this, a path owned by @org/team that isn't in
	// any stack approval rule never gets expanded and the gate always fails.
	if len(coResolved) > 0 {
		coOwners := make(map[string]struct{})
		for _, owners := range coResolved {
			for _, o := range owners {
				coOwners[o] = struct{}{}
			}
		}
		var coApprovers []string
		for o := range coOwners {
			coApprovers = append(coApprovers, o)
		}
		stackRules = append(stackRules, approvals.Rules{Approvers: coApprovers})
	}
	teamMembers, err := approvals.ExpandTeams(ctx, in.VCS, stackRules...)
	if err != nil {
		// Partial expansion is fine - log slugs that failed but proceed.
		// The unresolved slugs fall back to literal-match (which never
		// fires), so the gate fails closed for them.
		slog.Warn("team expansion partial", "err", err)
	}
	for slug, members := range teamMembers {
		slog.Debug("team expanded", "slug", slug, "members", members)
	}
	if len(teamMembers) == 0 {
		slog.Debug("team expansion returned no members - approvals will use literal matching only")
	}

	now := time.Now()

	// Break-glass authorization. Head-resolved: the config and CODEOWNERS
	// used here come from the PR HEAD (self-add is by design); the audit
	// record flags same-PR modification of the authorizing files below.
	// Fail-closed: unconfigured, unsupported-source, or unauthorized all
	// stop the run here - before any lock, credential, or engine call.
	var bgDecision *breakglass.Decision
	var bgTouched []string
	if in.BreakGlass != nil {
		bgTouched = breakglass.AuthorizingPathsTouched(changed)
		dec, bgErr := breakglass.Authorize(bgCfg, breakglass.Inputs{
			Actor:                   in.Actor,
			OwnedPaths:              coResolved,
			TeamMembers:             teamMembers,
			AuthorizingPathsTouched: bgTouched,
		})
		if bgErr != nil {
			timeline.add(ctx, "⛔", "break-glass refused", bgErr.Error())
			outcome = "blocked"
			return nil, fmt.Errorf("break-glass authorization: %w", bgErr)
		}
		if !dec.Authorized {
			detail := fmt.Sprintf("actor %s is not authorized: %s", in.Actor, strings.Join(dec.Trace, "; "))
			timeline.add(ctx, "⛔", "break-glass denied", detail)
			outcome = "blocked"
			return nil, fmt.Errorf("break-glass denied: %s", detail)
		}
		bgDecision = &dec
		slog.Warn("BREAK-GLASS apply authorized",
			"actor", in.Actor, "source", dec.Source,
			"justification", in.BreakGlass.Justification,
			"authorizing_config_modified_in_pr", len(bgTouched) > 0)
		detail := fmt.Sprintf("actor %s authorized via %s — justification: %q", in.Actor, dec.Source, in.BreakGlass.Justification)
		if len(bgTouched) > 0 {
			detail += fmt.Sprintf(" — ⚠️ authorizing config modified in this PR (%s)", strings.Join(bgTouched, ", "))
		}
		timeline.add(ctx, "🚨", "break-glass override", detail)

		// Break-glass intent audit: an emergency override MUST leave a
		// durable trace even if the process dies mid-apply, so the intent
		// entry is written BEFORE any lock, credential, or engine call - and
		// its write is a hard requirement: if the audit store can't record
		// the intent, the run refuses to start. ErrPreconditionFailed means
		// the entry already exists (a retried run) - already recorded, fine.
		if in.AuditWriter == nil {
			outcome = "blocked"
			return nil, errors.New("break-glass apply requires an audit writer; refusing to run without a durable audit trail")
		}
		intent := audit.Entry{
			RunID:      runID + "-intent",
			Op:         "apply",
			StartedAt:  start.UTC().Format(time.RFC3339),
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
			Actor:      in.Actor,
			PR:         in.PRNumber,
			CommitSHA:  in.CommitSHA,
			Repo:       in.RepoFull,
			RunURL:     in.CIRunURL,
			Outcome:    "break_glass_intent",
			BreakGlass: &audit.BreakGlass{
				Justification:             in.BreakGlass.Justification,
				AuthorizedVia:             bgDecision.Source,
				AuthorizingConfigModified: len(bgTouched) > 0,
				AuthorizingPathsModified:  bgTouched,
			},
		}
		if err := in.AuditWriter.Write(ctx, intent); err != nil && !errors.Is(err, blob.ErrPreconditionFailed) {
			timeline.add(ctx, "⛔", "break-glass refused", "intent audit entry could not be written; refusing to apply")
			return nil, fmt.Errorf("break-glass intent audit write failed; refusing to apply: %w", err)
		}
	}

	// Notify approved + applying before the loop (creates the message on
	// the apply trigger path). A break-glass run emits break_glass instead
	// of approved - approvals were bypassed, not granted.
	if in.PRNumber > 0 && in.Notifications != nil {
		channels := BuildNotifyChannels(ctx, in.Notifications, in.Blob, in.VCS)
		preSummaries := make([]summary.StackSummary, 0, len(target))
		for _, s := range target {
			preSummaries = append(preSummaries, summary.StackSummary{
				Project: s.Project, Stack: s.Name, Env: s.Env,
			})
		}
		preInput := PRNotifyInput{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunURL: in.CIRunURL,
			PRTitle: pr.Title, PRAuthor: pr.Author, Stacks: preSummaries,
		}
		if bgDecision != nil {
			if err := NotifyPREvent(ctx, channels, notify.EventBreakGlass, preInput); err != nil {
				slog.Warn("notify break_glass failed", "err", err, "pr", in.PRNumber)
			}
		} else {
			if err := NotifyPREvent(ctx, channels, notify.EventApproved, preInput); err != nil {
				slog.Warn("notify approved failed", "err", err, "pr", in.PRNumber)
			}
		}
		if err := NotifyPREvent(ctx, channels, notify.EventApplying, preInput); err != nil {
			slog.Warn("notify applying failed", "err", err, "pr", in.PRNumber)
		}
	}

	// Plan locking is on when config asks for it AND the engine can execute
	// a saved plan AND there is a bucket to have stored one in. Resolved
	// once: a per-stack answer would let one stack apply a locked plan while
	// another silently re-planned.
	planLocking := in.Blob != nil && !in.Refresh && PlanLockingEnabled(in.Config) && EngineSupportsSavedPlans(in.Engine)
	if !planLocking && !in.Refresh && PlanLockingEnabled(in.Config) && in.Blob != nil {
		slog.Info("plan locking requested but the engine cannot execute a saved plan; applies will re-plan",
			"engine", in.Engine.Name())
	}
	if in.Refresh {
		timeline.add(ctx, "🔄", "refresh requested",
			"state is reconciled with live infrastructure before this apply; the applied change set is computed after the refresh, so it is not the plan the PR previewed")
	}

	// 3. Per-stack: acquire lock → eval gates → apply or block.
	summaries := make([]summary.StackSummary, 0, len(target))
	anyBlocked := false
	overriddenSeen := map[preconditions.GateID]bool{}
	var overriddenGates []string
	for _, s := range target {
		ss := summary.StackSummary{
			Project: s.Project, Stack: s.Name, Env: s.Env,
		}

		// Cancelled (SIGINT/SIGTERM or deadline): stop starting new stacks.
		// The stack that was mid-apply has already been marked failed; the
		// remaining ones are recorded as failed too so the run exits nonzero
		// and the operator can see they never applied. No lock was acquired
		// for them, so there is nothing to release.
		if ctx.Err() != nil {
			ss.Status = summary.StatusError
			ss.Error = "run cancelled before this stack was applied: " + ctx.Err().Error()
			summaries = append(summaries, ss)
			continue
		}

		// Lock acquire. Holder identity is PR+RunID: a concurrent run of
		// this same PR (double `/reeve apply`, workflow re-run) is refused
		// with ErrHeldBySamePR and must not proceed to apply.
		lock, acquired, err := in.Locks.TryAcquire(ctx, s.Project, s.Name, corelocks.Holder{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunID: runID, Actor: in.Actor,
		}, ttl)
		if errors.Is(err, corelocks.ErrHeldBySamePR) {
			holderRun := "unknown"
			expiry := "unknown"
			if lock.Holder != nil {
				holderRun = lock.Holder.RunID
				if lock.Holder.ExpiresAt != "" {
					expiry = lock.Holder.ExpiresAt
				}
			}
			ss.Status = summary.StatusBlocked
			ss.Error = fmt.Sprintf("another run of PR #%d (%s) currently holds the lock for this stack; wait for it to finish or for its lease to expire at %s", in.PRNumber, holderRun, expiry)
			anyBlocked = true
			slog.Info("lock held by concurrent run of same PR",
				"stack", s.Ref(), "pr", in.PRNumber, "holder_run", holderRun, "this_run", runID)
			summaries = append(summaries, ss)
			continue
		}
		if err != nil {
			ss.Status = summary.StatusError
			ss.Error = fmt.Sprintf("lock acquire: %v", err)
			summaries = append(summaries, ss)
			continue
		}
		lockBlockedBy := 0
		if !acquired && lock.Holder != nil {
			lockBlockedBy = lock.Holder.PR
		}

		// Resolve rules + approvals for this stack. TeamMembers is the
		// pre-resolved expansion shared across all stacks in this run.
		rules := approvals.Resolve(appCfg, s.Ref())
		rules.TeamMembers = teamMembers
		approvalsRes := approvals.Evaluate(rules, rawApprovals, approvals.PR{
			Number: in.PRNumber, HeadSHA: approvalHeadSHA, Author: pr.Author,
			RepoPrivate: pr.RepoPrivate,
		}, coResolved, pr.Author, now)
		slog.Debug("approvals evaluated",
			"stack", s.Ref(),
			"satisfied", approvalsRes.Satisfied,
			"got", approvalsRes.Got,
			"needed", approvalsRes.TotalNeeded,
			"missing", approvalsRes.Missing,
			"trace", approvalsRes.Trace,
		)

		// Freeze check. The window name flows into preconditions.Inputs so
		// the gate's failure reason can identify which window blocked.
		inFreeze := false
		freezeName := ""
		if name, active, ferr := freezeActiveFor(freezeCfg, s.Ref(), now); ferr != nil {
			// Fail closed: if a window covering this stack can't be evaluated
			// (e.g. a bad cron expression), block rather than silently apply
			// through a freeze we couldn't check. Only the stacks that window
			// covers are affected.
			slog.Warn("freeze evaluation failed; blocking stack", "stack", s.Ref(), "err", ferr)
			inFreeze = true
			freezeName = "freeze evaluation failed: " + ferr.Error()
		} else if active {
			inFreeze = true
			freezeName = name
		}

		// Look up the prior preview manifest from blob for this SHA + stack.
		prev, lookupErr := FindPreviewForStack(ctx, in.Blob, in.PRNumber, in.CommitSHA, s.Ref())
		if lookupErr != nil {
			// Not fatal - treat as "no preview" so the gate fails cleanly.
			slog.Debug("preview lookup failed", "stack", s.Ref(), "sha", in.CommitSHA, "err", lookupErr)
			prev = PreviewStatus{}
		}
		slog.Debug("preview status", "stack", s.Ref(), "found", prev.Found, "succeeded", prev.Succeeded, "age", prev.Age)

		// Run policy hooks against the PREVIEW plan - the plan that will be
		// applied - not the still-empty pre-apply summary (which has no
		// counts or plan body yet, so any plan-content rule always passed).
		redactor := BuildRedactor(in.Shared)
		hooks := HooksFromEngine(in.Config)
		var policyPtr *bool
		if len(hooks) > 0 {
			policyTarget := ss
			if prev.Plan != nil {
				policyTarget = *prev.Plan
			}
			policyPassed, policyResults, policyErr := RunPolicyForStack(ctx, hooks, s, policyTarget, redactor)
			// Fail closed: if hooks are configured but there is no preview
			// plan to evaluate, or a hook failed to execute, the gate must
			// not pass. Applying with policy unevaluated defeats the gate.
			if prev.Plan == nil || policyErr != nil {
				if policyErr != nil {
					slog.Warn("policy execution failed", "stack", s.Ref(), "err", policyErr)
				} else {
					slog.Warn("policy skipped: no preview plan to evaluate", "stack", s.Ref())
				}
				policyPassed = false
			}
			policyPtr = &policyPassed
			if len(policyResults) > 0 {
				// Attach policy rendering into the PR comment via FullPlan
				// suffix - renderer will collapse it with other output.
				ss.FullPlan = ss.FullPlan + policyRender(policyResults)
			}
		}

		// Evaluate preconditions.
		pcInputs := preconditions.Inputs{
			StackRef:           s.Ref(),
			PRIsFork:           pr.IsFork,
			PRIsDraft:          pr.IsDraft,
			ForkOptInAllowed:   in.Shared != nil && in.Shared.Apply.AllowForkPRs,
			UpToDate:           upToDate,
			CommitsBehind:      behind,
			ChecksGreen:        checksGreen,
			HasFreshPreview:    prev.Found,
			PreviewAge:         prev.Age,
			PreviewSucceeded:   prev.Succeeded,
			PolicyPassed:       policyPtr,
			ApprovalsSatisfied: approvalsRes.Satisfied,
			LockAcquirable:     acquired,
			LockBlockedByPR:    lockBlockedBy,
			InFreeze:           inFreeze,
			FreezeName:         freezeName,

			BreakGlass:               bgDecision != nil,
			BreakGlassOverrideFreeze: bgCfg.OverrideFreeze,
		}
		pcResult := preconditions.Evaluate(preCfg, pcInputs)
		for _, g := range pcResult.Overridden {
			if !overriddenSeen[g] {
				overriddenSeen[g] = true
				overriddenGates = append(overriddenGates, string(g))
			}
		}
		ss.Gates = gatesToTrace(pcResult)
		ss.BlockedBy = lockBlockedBy
		slog.Debug("preconditions evaluated", "stack", s.Ref(), "blocked", pcResult.Blocked, "gates", ss.Gates)

		if pcResult.Blocked {
			if ss.Status == "" {
				ss.Status = summary.StatusBlocked
			}
			anyBlocked = true
			if acquired {
				releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "gates blocked")
			}
			summaries = append(summaries, ss)
			continue
		}

		// Gates green - acquire auth creds and run apply. authCleanup must
		// run before the loop iteration ends so on-disk credential
		// artefacts (e.g. GCP WIF token files) do not outlive their use.
		authEnv, authCleanup, aerr := ResolveAuthEnv(ctx, in.AuthConfig, in.AuthRegistry, s.Ref(), auth.ModeApply, LocalAuth{})
		if aerr != nil {
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(aerr.Error())
			releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "auth resolve failed")
			summaries = append(summaries, ss)
			continue
		}
		for _, v := range authEnv {
			redactor.AddSecret(v)
		}

		// Plan locking: execute the plan artifact this commit's preview
		// saved, so the engine cannot compute a wider change set at apply
		// time. Scope binding above already fixed WHICH stacks apply; this
		// fixes WHAT each apply does.
		//
		// A missing artifact does not block: manifests written before plan
		// locking existed have no PlanKey, and an operator can turn locking
		// off. It is reported on the timeline instead, because "this apply
		// re-planned" is exactly the thing an operator must not have to
		// infer.
		lockedPlan := ""
		planCleanup := func() {}
		if planLocking && (prev.Plan == nil || prev.Plan.PlanKey == "") {
			slog.Info("plan locking: no saved plan for this stack; applying a fresh plan",
				"stack", s.Ref(), "sha", in.CommitSHA)
			timeline.add(ctx, "⚠️", "plan lock unavailable", fmt.Sprintf(
				"%s: the preview for %s stored no plan artifact; this apply computed a new plan",
				s.Ref(), shortSHA(in.CommitSHA)))
		} else if planLocking {
			path, cleanup, ferr := FetchPlanArtifact(ctx, in.Blob, prev.Plan.PlanKey)
			if ferr != nil {
				slog.Warn("plan locking: saved plan could not be fetched; applying a fresh plan",
					"stack", s.Ref(), "key", prev.Plan.PlanKey, "err", ferr)
				timeline.add(ctx, "⚠️", "plan lock unavailable", fmt.Sprintf(
					"%s: saved plan %s could not be read (%v); this apply computed a new plan",
					s.Ref(), prev.Plan.PlanKey, ferr))
			} else {
				lockedPlan = path
				planCleanup = cleanup
			}
		}

		stackCtx, endStack := in.OTEL.StartStackSpan(ctx, s.Project, s.Name, s.Env, "apply")
		stackStart := time.Now()
		// Lease heartbeat: an apply longer than locking.ttl must not have
		// its LIVE holder reaped mid-flight (two applies would then run
		// concurrently). Refreshes every ttl/3 until the engine returns.
		stopHeartbeat := in.Locks.StartHeartbeat(stackCtx, s.Project, s.Name, corelocks.Holder{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunID: runID, Actor: in.Actor,
		}, ttl)
		res, aerr := in.Engine.Apply(stackCtx, s, iac.ApplyOpts{
			Cwd:        absJoin(in.RepoRoot, s.Path),
			Env:        authEnv,
			TimeoutSec: ApplyTimeoutSec(in.Config),
			PlanPath:   lockedPlan,
			Refresh:    in.Refresh,
		})
		stopHeartbeat()
		authCleanup()
		// The plan file is a copy of the engine's change set on the runner's
		// disk; drop it as soon as the engine is done with it rather than at
		// the end of the run.
		planCleanup()
		stackOutcome := "success"
		if aerr != nil {
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(aerr.Error())
			stackOutcome = "error"
			endStack(stackOutcome, time.Since(stackStart).Seconds())
			releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "engine apply failed")
			summaries = append(summaries, ss)
			PostAnnotation(ctx, in.Annotations, annotations.EventApplyFailed,
				s.Project, s.Name, s.Env, "failed", ss.Error, in.PRNumber, in.CommitSHA)
			continue
		}
		endStack(stackOutcome, time.Since(stackStart).Seconds())
		ss.Counts = res.Counts
		ss.DurationMS = res.DurationMS
		ss.FullPlan = redactor.Redact(res.Output)
		in.OTEL.RecordStackChanges(ctx, s.Project, s.Name, res.Counts.Add, res.Counts.Change, res.Counts.Delete, res.Counts.Replace)
		if res.Error != "" {
			ss.Status = summary.StatusError
			ss.Error = redactor.Redact(res.Error)
		} else if res.Counts.Total() == 0 {
			ss.Status = summary.StatusNoOp
		} else {
			ss.Status = summary.StatusPlanned
		}
		releaseLockOrLog(ctx, in.Locks, s.Project, s.Name, in.PRNumber, runID, ttl, "stack apply complete")
		summaries = append(summaries, ss)
	}

	// 4. Render apply comment.
	sortMode := "status_grouped"
	if in.Shared != nil && in.Shared.Comments.Sort != "" {
		sortMode = in.Shared.Comments.Sort
	}
	commentStyle := "replace"
	if in.Shared != nil && in.Shared.Comments.Style != "" {
		commentStyle = in.Shared.Comments.Style
	}
	dur := int(time.Since(start).Seconds())
	var bgNote *render.BreakGlassNote
	if bgDecision != nil {
		bgNote = &render.BreakGlassNote{
			Actor:              in.Actor,
			Justification:      in.BreakGlass.Justification,
			AuthorizedVia:      bgDecision.Source,
			Overridden:         overriddenGates,
			ConfigModifiedInPR: len(bgTouched) > 0,
		}
	}
	body := render.Apply(render.ApplyInput{
		RunNumber:   in.RunNumber,
		CommitSHA:   in.CommitSHA,
		DurationSec: dur,
		CIRunURL:    in.CIRunURL,
		Stacks:      summaries,
		SortMode:    sortMode,
		Style:       commentStyle,
		StackView:   stackView(in.Shared),
		BreakGlass:  bgNote,
	})

	// Terminal persistence: once the run context has been cancelled the
	// remaining writes (manifest, timeline, comment, notify, audit) run on a
	// short detached deadline so the run's outcome is still recorded before
	// the CI runner kills the process.
	pctx, endTerminal := terminalContext(ctx)
	defer endTerminal()

	// 5. Write run manifest. A failure here must NOT short-circuit the
	// terminal reporting below: infrastructure has (potentially) already
	// changed, so the audit entry, timeline event, and PR comment are still
	// written best-effort - each independently - and the run exits nonzero
	// at the end with a clear message.
	manifestErr := writeApplyManifest(pctx, in.Blob, in.PRNumber, runID, summaries, in.CommitSHA)
	if manifestErr != nil {
		slog.Error("apply manifest persistence failed - infra may have changed; recording outcome on the PR anyway",
			"err", manifestErr, "run_id", runID, "pr", in.PRNumber)
		timeline.add(pctx, "⚠️", "manifest persistence failed", manifestErr.Error())
		body += fmt.Sprintf("\n\n> [!WARNING]\n> **Run manifest persistence failed.** The stack results above reflect what actually applied, but the run manifest could not be written to blob storage (`%v`). The run exits nonzero; investigate the storage backend before re-running.", manifestErr)
	}

	// 5b. Timeline terminal event + applied-state pointer. Only a fully clean
	// run (no failures, nothing blocked) records the applied marker, so a
	// blocked/failed run can be safely re-run without tripping the guard.
	finalOutcome := aggregateOutcome(summaries, anyBlocked)
	switch finalOutcome {
	case "failed":
		timeline.add(pctx, "🔴", "failed", failedStacksDetail(summaries))
	case "blocked":
		timeline.add(pctx, "🔒", "blocked", blockedStacksDetail(summaries))
	default:
		timeline.add(pctx, "✅", "applied", changedStacksDetail(summaries))
		// The run is fully done - leave every lock the PR still appears in
		// (queues on stacks a previous run wanted but this one no longer
		// targets, plus the just-released holders as no-ops). RunID-scoped,
		// so a different live run of the same PR keeps its holds.
		if in.Locks != nil && in.PRNumber > 0 {
			// force=true: this is the finishing run clearing its own
			// runID-scoped entries; its lease may still look active.
			if n, _, err := in.Locks.UnlockPRAll(pctx, in.PRNumber, runID, ttl, true); err != nil {
				slog.Warn("lock unlock sweep failed", "pr", in.PRNumber, "err", err)
			} else if n > 0 {
				slog.Info("removed lock entries after successful apply", "pr", in.PRNumber, "locks", n)
			}
		}
		if err := writeAppliedState(pctx, in.Blob, AppliedState{
			CommitSHA: in.CommitSHA, RunID: runID, RunNumber: in.RunNumber,
			AppliedAt: time.Now().UTC().Format(time.RFC3339), PR: in.PRNumber,
		}); err != nil {
			// Non-fatal: the apply shipped; a missing pointer only means a
			// future re-run won't be auto-skipped.
			slog.Warn("write applied-state failed", "err", err, "sha", in.CommitSHA)
		}
	}

	// 6. Post PR comment. A comment failure no longer short-circuits the
	// notify + audit steps below (each terminal write is independent); it is
	// carried to the final return so the run still exits nonzero.
	var commentErr error
	if in.VCS != nil && in.PRNumber > 0 {
		var cerr error
		switch commentStyle {
		case "append":
			cerr = in.VCS.PostComment(pctx, in.PRNumber, body)
		case "section":
			cerr = in.VCS.UpsertComment(pctx, in.PRNumber, body, render.ApplyMarker)
		default:
			cerr = in.VCS.UpsertComment(pctx, in.PRNumber, body, render.Marker)
		}
		if cerr != nil {
			commentErr = fmt.Errorf("post pr comment: %w", cerr)
			slog.Error("apply PR comment failed - continuing terminal reporting",
				"err", cerr, "pr", in.PRNumber, "run_id", runID)
		}
	}

	// 7. Notifications (run last, capture everything above). The apply has
	// already shipped at this point; a notification failure must not abort
	// the run.
	if in.PRNumber > 0 && in.Notifications != nil {
		channels := BuildNotifyChannels(pctx, in.Notifications, in.Blob, in.VCS)
		ev := ApplyOutcomeEvent(summaries, anyBlocked)
		if err := NotifyPREvent(pctx, channels, ev, PRNotifyInput{
			PR: in.PRNumber, CommitSHA: in.CommitSHA, RunURL: in.CIRunURL,
			PRTitle: pr.Title, PRAuthor: pr.Author, Stacks: summaries,
		}); err != nil {
			slog.Warn("notify applied failed", "err", err, "pr", in.PRNumber, "event", ev)
		}
	}

	// 8. Audit log. Side effects (apply, PR comment, Slack) have already
	// shipped, so a write failure is logged loudly but not propagated -
	// returning an error here would falsely tell the caller the run failed.
	if in.AuditWriter != nil {
		entry := audit.Entry{
			RunID:      runID,
			Op:         "apply",
			StartedAt:  start.UTC().Format(time.RFC3339),
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
			Actor:      in.Actor,
			PR:         in.PRNumber,
			CommitSHA:  in.CommitSHA,
			Repo:       in.RepoFull,
			RunURL:     in.CIRunURL,
			Outcome:    aggregateOutcome(summaries, anyBlocked),
			Stacks:     toAuditStacks(summaries),
			DurationMS: time.Since(start).Milliseconds(),
		}
		if bgDecision != nil {
			entry.BreakGlass = &audit.BreakGlass{
				Justification:             in.BreakGlass.Justification,
				AuthorizedVia:             bgDecision.Source,
				OverriddenGates:           overriddenGates,
				AuthorizingConfigModified: len(bgTouched) > 0,
				AuthorizingPathsModified:  bgTouched,
			}
		}
		if err := in.AuditWriter.Write(pctx, entry); err != nil && !errors.Is(err, blob.ErrPreconditionFailed) {
			slog.Error("audit write failed - run already shipped",
				"err", err, "run_id", runID, "pr", in.PRNumber)
		}
	}

	outcome = aggregateOutcome(summaries, anyBlocked)
	runEvent := annotations.EventApplyCompleted
	if outcome == "failed" {
		runEvent = annotations.EventApplyFailed
	}
	PostAnnotation(pctx, in.Annotations, runEvent,
		"", "", "", outcome, "", in.PRNumber, in.CommitSHA)

	failedRefs := failedStackRefs(summaries)
	result := &ApplyOutput{
		Stacks:       summaries,
		CommentBody:  body,
		RunID:        runID,
		DurationSec:  dur,
		Blocked:      anyBlocked,
		Failed:       len(failedRefs) > 0,
		FailedStacks: failedRefs,
	}
	// Post-apply persistence failure: everything above was still reported
	// best-effort (timeline, comment, notify, audit), but the run must exit
	// nonzero - infra changed and part of the durable record is missing.
	if perr := errors.Join(manifestErr, commentErr); perr != nil {
		return result, fmt.Errorf("apply run finished (outcome=%s) but post-apply persistence failed: %w", outcome, perr)
	}
	return result, nil
}

// failedStackRefs returns the refs of stacks whose apply errored, in run
// order. Non-empty means the run must surface a nonzero exit.
func failedStackRefs(ss []summary.StackSummary) []string {
	var refs []string
	for _, s := range ss {
		if s.Status == summary.StatusError {
			refs = append(refs, s.Ref())
		}
	}
	return refs
}

func gatesToTrace(r preconditions.Result) []summary.GateTrace {
	out := make([]summary.GateTrace, 0, len(r.Gates))
	for _, g := range r.Gates {
		out = append(out, summary.GateTrace{
			Gate:    string(g.Gate),
			Outcome: string(g.Outcome),
			Reason:  g.Reason,
		})
	}
	return out
}

// terminalGrace bounds best-effort terminal writes (lock release, status
// comment, audit entry) once the run context has been cancelled. GitHub
// Actions sends SIGTERM and SIGKILLs ~7.5s later, so the budget must stay
// comfortably under that window.
const terminalGrace = 5 * time.Second

// terminalContext returns ctx unchanged while the run is alive. Once ctx
// has been cancelled (signal, deadline), it returns a detached context with
// a short deadline so terminal best-effort writes can still land before the
// process is killed - a cancelled Actions job must release its locks, not
// pin them for the full lease ttl.
func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), terminalGrace)
}

// releaseLockOrLog releases a per-stack lock and logs a warning on failure
// instead of swallowing the error. The lock-release path needs to be
// best-effort (the work has already happened or been declined), but a silent
// drop hid leaks until the reaper noticed - operators need a signal.
// Release is RunID-scoped: only the run that holds the lock frees it, so a
// concurrent run of the same PR cannot have its lease pulled out from under
// it. ttl bounds the lease of any holder promoted from the queue. A
// cancelled run context is swapped for a short detached one so the release
// still lands during the CI kill grace window.
func releaseLockOrLog(ctx context.Context, store *blocks.Store, project, stack string, pr int, runID string, ttl time.Duration, reason string) {
	if store == nil {
		return
	}
	rctx, cancel := terminalContext(ctx)
	defer cancel()
	if _, err := store.Release(rctx, project, stack, pr, runID, ttl); err != nil {
		slog.Warn("lock release failed",
			"project", project, "stack", stack, "pr", pr, "run_id", runID, "reason", reason, "err", err)
	}
}

func writeApplyManifest(ctx context.Context, store blob.Store, pr int, runID string, stacks []summary.StackSummary, sha string) error {
	if store == nil {
		return nil
	}
	m := manifest{
		RunID: runID, PR: pr, CommitSHA: sha, Op: "apply",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Stacks:    stacks,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("runs/pr-%d/%s/manifest.json", pr, runID)
	if pr == 0 {
		key = fmt.Sprintf("runs/local/%s/manifest.json", runID)
	}
	_, err = store.Put(ctx, key, bytes.NewReader(data))
	return err
}

func toAuditStacks(ss []summary.StackSummary) []audit.Stack {
	out := make([]audit.Stack, 0, len(ss))
	for _, s := range ss {
		out = append(out, audit.Stack{
			Ref:    s.Ref(),
			Env:    s.Env,
			Status: string(s.Status),
			Add:    s.Counts.Add, Change: s.Counts.Change,
			Delete: s.Counts.Delete, Replace: s.Counts.Replace,
			DurationMS: s.DurationMS,
			Error:      s.Error,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func aggregateOutcome(ss []summary.StackSummary, anyBlocked bool) string {
	for _, s := range ss {
		if s.Status == summary.StatusError {
			return "failed"
		}
	}
	if anyBlocked {
		return "blocked"
	}
	return "success"
}

// scopeDelta returns the refs present in mapped but absent from bound - the
// stacks a freshly-computed change mapping would have applied that the plan
// for this commit never covered.
func scopeDelta(mapped, bound []discovery.Stack) []string {
	have := make(map[string]bool, len(bound))
	for _, s := range bound {
		have[s.Ref()] = true
	}
	var out []string
	for _, s := range mapped {
		if !have[s.Ref()] {
			out = append(out, s.Ref())
		}
	}
	sort.Strings(out)
	return out
}
