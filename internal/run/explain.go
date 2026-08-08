package run

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/core/discovery"
	corelocks "github.com/reeveops/reeve/internal/core/locks"
	"github.com/reeveops/reeve/internal/core/preconditions"
	"github.com/reeveops/reeve/internal/core/render"
	"github.com/reeveops/reeve/internal/vcs"
	"github.com/reeveops/reeve/internal/vcs/codeowners"
)

// explainVCS is the read-only VCS surface explain needs: PR facts,
// approvals, CODEOWNERS, team expansion, and the comment upsert. No write
// beyond the comment itself.
type explainVCS interface {
	commentPoster
	GetPR(ctx context.Context, number int) (*vcs.PR, error)
	ListChangedFiles(ctx context.Context, number int) ([]string, error)
	ChecksGreen(ctx context.Context, sha string, opts vcs.ChecksGreenOpts) (bool, []string, error)
	CompareBranches(ctx context.Context, base, head string) (int, error)
	approvals.Source
	ListCommentApprovals(ctx context.Context, pr approvals.PR, cfg vcs.CommentApprovalConfig) ([]approvals.Approval, error)
	FetchCodeowners(ctx context.Context) (string, error)
	ListTeamMembers(ctx context.Context, slug string) ([]string, error)
}

// ExplainInput wires dependencies for a report-only run.
type ExplainInput struct {
	PRNumber       int
	CommitSHA      string
	CIRunID        int64
	CIRunURL       string
	SelfCheckNames []string
	RepoRoot       string
	Engine         Engine
	Config         *schemas.Engine
	Shared         *schemas.Shared
	Blob           blob.Store
	Locks          *blocks.Store
	VCS            explainVCS
	// StackFilter limits the report to one project/stack ref. Empty covers
	// every stack the PR maps to.
	StackFilter string
	// CommentApproval configures the opt-in pr_comment approval source,
	// mirroring apply.
	CommentApproval vcs.CommentApprovalConfig
}

// ExplainOutput carries the rendered body (also posted when PRNumber > 0).
type ExplainOutput struct {
	Body    string
	Blocked bool
}

// Explain answers "why?" from the PR: per stack, the resolved approval
// rules, the lock state from a plain read, and a full gate trace from a
// report-only evaluation. It never invokes an engine, acquires a lock,
// exchanges per-stack cloud credentials, or writes state - the only side
// effect is the comment upsert. It does read reeve's own state bucket
// (preview manifests, locks) with whatever bucket credentials the runner
// already holds.
func Explain(ctx context.Context, in ExplainInput) (*ExplainOutput, error) {
	// 1. Resolve the PR's stacks - same discovery pipeline as preview.
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
	cm := changeMappingFromConfig(in.Config)
	target := discovery.AffectedDetailed(declared, changed, cm).Stacks

	// PR facts come before the stack filter so the error comment below and
	// the success comment share one marker keyed to the PR HEAD: on
	// issue_comment events $GITHUB_SHA is the base branch, so the caller's
	// best-effort SHA is overridden the same way apply does it.
	pr, err := in.VCS.GetPR(ctx, in.PRNumber)
	if err != nil {
		return nil, fmt.Errorf("get pr: %w", err)
	}
	commitSHA := in.CommitSHA
	if pr.HeadSHA != "" {
		commitSHA = pr.HeadSHA
	}

	if in.StackFilter != "" {
		var picked []discovery.Stack
		for _, s := range declared {
			if s.Ref() == in.StackFilter {
				picked = []discovery.Stack{s}
				break
			}
		}
		if picked == nil {
			refs := make([]string, 0, len(declared))
			for _, s := range declared {
				refs = append(refs, s.Ref())
			}
			sort.Strings(refs)
			msg := fmt.Sprintf("unknown stack %q; valid refs: %s", in.StackFilter, strings.Join(refs, ", "))
			// The asker is in the PR, so the answer goes to the PR: post
			// the error as the explain comment rather than only failing
			// the run.
			if in.PRNumber > 0 {
				body := render.ExplainMarker(shortSHA(commitSHA)) + "\n### 🔎 reeve · explain\n\n❌ " + msg + "\n"
				if perr := in.VCS.UpsertComment(ctx, in.PRNumber, body, render.ExplainMarker(shortSHA(commitSHA))); perr != nil {
					slog.Warn("post explain error comment failed", "err", perr)
				}
			}
			return nil, fmt.Errorf("%s", msg)
		}
		target = picked
	}
	if len(target) == 0 {
		return nil, fmt.Errorf("this PR maps to no stacks; pass an explicit project/stack to explain one anyway")
	}

	// 2. Remaining PR-level facts, all plain reads. Explain is a diagnostic:
	// a failed read of a gate input degrades that gate to "could not be
	// evaluated" instead of killing the whole report - the commenter asking
	// "why?" still gets an answer for everything that could be read.
	checksGreen, _, cerr := in.VCS.ChecksGreen(ctx, commitSHA, vcs.ChecksGreenOpts{
		IgnoreRunID: in.CIRunID, IgnoreNames: in.SelfCheckNames,
	})
	checksReason := ""
	if cerr != nil {
		slog.Warn("explain: checks_green unavailable", "err", cerr)
		checksGreen = false
		checksReason = "could not be evaluated: " + cerr.Error()
	}
	behind, berr := in.VCS.CompareBranches(ctx, pr.BaseRef, commitSHA)
	upToDateReason := ""
	if berr != nil {
		slog.Warn("explain: branch comparison unavailable", "base", pr.BaseRef, "err", berr)
		behind = -1 // fail the gate closed; the reason override below says why
		upToDateReason = "could not be evaluated: " + berr.Error()
	}

	appCfg := toApprovalsConfig(in.Shared)
	approvalHeadSHA := pr.HeadSHA
	if approvalHeadSHA == "" {
		approvalHeadSHA = commitSHA
	}
	approvalPR := approvals.PR{
		Number: in.PRNumber, HeadSHA: approvalHeadSHA, Author: pr.Author, Changed: changed,
	}
	var reviewApprovals, commentApprovals []approvals.Approval
	if appCfg.PRReviewEnabled() {
		if reviewApprovals, err = in.VCS.ListApprovals(ctx, approvalPR); err != nil {
			return nil, fmt.Errorf("list approvals: %w", err)
		}
	}
	if appCfg.PRCommentEnabled() {
		commentCfg := in.CommentApproval
		if commentCfg.Command == "" {
			commentCfg.Command = appCfg.CommentCommand()
		}
		if commentApprovals, err = in.VCS.ListCommentApprovals(ctx, approvalPR, commentCfg); err != nil {
			return nil, fmt.Errorf("list comment approvals: %w", err)
		}
	}
	rawApprovals := approvals.MergeApprovals(reviewApprovals, commentApprovals)

	coContent, err := in.VCS.FetchCodeowners(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch codeowners: %w", err)
	}
	var coResolved map[string][]string
	if coContent != "" {
		coResolved = codeowners.Resolve(codeowners.Parse(strings.NewReader(coContent)), changed)
	}

	stackRules := make([]approvals.Rules, 0, len(target))
	for _, s := range target {
		stackRules = append(stackRules, approvals.Resolve(appCfg, s.Ref()))
	}
	teamMembers, err := approvals.ExpandTeams(ctx, in.VCS, stackRules...)
	if err != nil {
		slog.Warn("team expansion partial", "err", err)
	}

	freezeCfg := toFreezeConfig(in.Shared)
	preCfg := toPreconditionsConfig(in.Shared)
	hooksConfigured := len(HooksFromEngine(in.Config)) > 0
	now := time.Now()

	// 3. Per stack: rules + lock read + report-only gates.
	out := render.ExplainInput{CommitSHA: commitSHA, RunURL: in.CIRunURL}
	blocked := false
	for _, s := range target {
		rules := approvals.Resolve(appCfg, s.Ref())
		rules.TeamMembers = teamMembers
		approvalsRes := approvals.Evaluate(rules, rawApprovals, approvals.PR{
			Number: in.PRNumber, HeadSHA: approvalHeadSHA, Author: pr.Author,
			RepoPrivate: pr.RepoPrivate,
		}, coResolved, pr.Author, now)

		inFreeze := false
		freezeName := ""
		if name, active, ferr := freezeActiveFor(freezeCfg, s.Ref(), now); ferr != nil {
			inFreeze = true
			freezeName = "freeze evaluation failed: " + ferr.Error()
		} else if active {
			inFreeze = true
			freezeName = name
		}

		prev, lookupErr := FindPreviewForStack(ctx, in.Blob, in.PRNumber, commitSHA, s.Ref())
		if lookupErr != nil {
			slog.Warn("explain: preview lookup failed", "stack", s.Ref(), "err", lookupErr)
			prev = PreviewStatus{}
		}

		// Lock: a plain read. Acquirable means free/expired, or already
		// held by this PR (a re-run would be refused, but the lock is not
		// what blocks this PR).
		es := render.ExplainStack{Ref: s.Ref()}
		lockAcquirable := true
		lockBlockedBy := 0
		if in.Locks != nil {
			lock, _, lerr := in.Locks.Get(ctx, s.Project, s.Name)
			if lerr != nil {
				return nil, fmt.Errorf("read lock %s: %w", s.Ref(), lerr)
			}
			status := lock.Status(now)
			es.LockStatus = string(status)
			if lock.Holder != nil {
				es.LockHolderPR = lock.Holder.PR
				es.LockHolderRun = lock.Holder.RunID
				es.LockAcquiredAt = lock.Holder.AcquiredAt
				es.LockExpiresAt = lock.Holder.ExpiresAt
			}
			for _, q := range lock.Queue {
				es.LockQueuePRs = append(es.LockQueuePRs, q.PR)
			}
			if status == corelocks.StatusHeld && lock.Holder != nil && lock.Holder.PR != in.PRNumber {
				lockAcquirable = false
				lockBlockedBy = lock.Holder.PR
			}
		} else {
			es.LockStatus = "unknown (no lock store)"
		}

		pcResult := preconditions.EvaluateAll(preCfg, preconditions.Inputs{
			StackRef:           s.Ref(),
			PRIsFork:           pr.IsFork,
			PRIsDraft:          pr.IsDraft,
			ForkOptInAllowed:   in.Shared != nil && in.Shared.Apply.AllowForkPRs,
			UpToDate:           behind == 0,
			CommitsBehind:      behind,
			ChecksGreen:        checksGreen,
			HasFreshPreview:    prev.Found,
			PreviewAge:         prev.Age,
			PreviewSucceeded:   prev.Succeeded,
			PolicyPassed:       nil, // report-only: hooks are never executed here
			ApprovalsSatisfied: approvalsRes.Satisfied,
			LockAcquirable:     lockAcquirable,
			LockBlockedByPR:    lockBlockedBy,
			InFreeze:           inFreeze,
			FreezeName:         freezeName,
		})
		es.Gates = gatesToTrace(pcResult)
		// PolicyPassed nil renders as "no policy hooks configured"; when
		// hooks exist that reason is wrong - explain deliberately does not
		// execute them. Say so.
		for i := range es.Gates {
			switch es.Gates[i].Gate {
			case string(preconditions.GatePolicy):
				if hooksConfigured {
					es.Gates[i].Reason = "policy hooks run at preview/apply time - not executed by explain"
				}
			case string(preconditions.GateChecksGreen):
				if checksReason != "" {
					es.Gates[i].Reason = checksReason
				}
			case string(preconditions.GateUpToDate):
				if upToDateReason != "" {
					es.Gates[i].Reason = upToDateReason
				}
			}
		}
		if pcResult.Blocked {
			blocked = true
		}

		es.RequiredApprovals = rules.RequiredApprovals
		es.RequireAllGroups = rules.RequireAllGroups
		es.Codeowners = rules.Codeowners
		es.DismissOnNewCommit = rules.DismissOnNewCommit
		es.Approvers = rules.Approvers
		es.ApprovalsGot = approvalsRes.Got
		es.ApprovalsNeeded = approvalsRes.TotalNeeded
		es.ApprovalsMissing = approvalsRes.Missing
		out.Stacks = append(out.Stacks, es)
	}

	// 4. Render, redact, post. One comment per commit, edited in place.
	body := BuildRedactor(in.Shared).Redact(render.Explain(out))
	if in.PRNumber > 0 {
		if err := in.VCS.UpsertComment(ctx, in.PRNumber, body, render.ExplainMarker(shortSHA(commitSHA))); err != nil {
			return nil, fmt.Errorf("post explain comment: %w", err)
		}
	}
	return &ExplainOutput{Body: body, Blocked: blocked}, nil
}
