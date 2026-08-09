package run

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/vcs"
	"github.com/reeveops/reeve/internal/vcs/codeowners"
)

// gateInputVCS is the VCS surface needed to gather gate inputs. Both
// applyVCS and explainVCS satisfy it.
type gateInputVCS interface {
	ChecksGreen(ctx context.Context, sha string, opts vcs.ChecksGreenOpts) (bool, []string, error)
	CompareBranches(ctx context.Context, base, head string) (int, error)
	approvals.Source
	ListCommentApprovals(ctx context.Context, pr approvals.PR, cfg vcs.CommentApprovalConfig) ([]approvals.Approval, error)
	FetchCodeowners(ctx context.Context) (string, error)
}

// gateInputs is everything the preconditions gates need that has to be read
// from the VCS, gathered once.
//
// Apply and explain must agree on these. Explain's entire job is answering
// "why is my apply blocked", which is only trustworthy if it evaluates the
// same inputs apply does - and when the two computed them separately, a new
// approval source or a changed head-SHA rule needed two edits to stay
// honest.
//
// The two callers do NOT agree on what to do when a read fails, so that
// stays with them. Apply fails closed: an API outage must never silently
// pass a gate. Explain degrades: it is a diagnostic, so a gate it could not
// evaluate is reported as such rather than killing the whole report. The
// errors whose handling differs are therefore recorded on the struct rather
// than returned; the ones both callers treat identically (approvals,
// CODEOWNERS - both fail closed) are returned directly.
type gateInputs struct {
	// ChecksErr and CompareErr are non-nil when that input could not be
	// read. The values alongside them are already set to the fail-closed
	// answer, so a caller that ignores the error still blocks.
	ChecksGreen   bool
	FailingChecks []string
	ChecksErr     error

	Behind     int
	CompareErr error

	ApprovalsCfg approvals.Config
	ApprovalPR   approvals.PR
	RawApprovals []approvals.Approval
	Codeowners   map[string][]string
}

// UpToDate reports whether the branch is level with its base.
func (g *gateInputs) UpToDate() bool { return g.Behind == 0 }

// gatherGateInputs reads the PR-level gate inputs shared by apply and
// explain. pr must already be fetched; commitSHA is the SHA under
// evaluation and changed the PR's changed-file list.
func gatherGateInputs(
	ctx context.Context,
	v gateInputVCS,
	shared *schemas.Shared,
	commentCfg vcs.CommentApprovalConfig,
	prNumber int,
	pr *vcs.PR,
	commitSHA string,
	changed []string,
	checksOpts vcs.ChecksGreenOpts,
) (*gateInputs, error) {
	g := &gateInputs{}

	g.ChecksGreen, g.FailingChecks, g.ChecksErr = v.ChecksGreen(ctx, commitSHA, checksOpts)
	if g.ChecksErr != nil {
		g.ChecksGreen = false // fail closed even if the caller ignores the error
	} else if !g.ChecksGreen {
		slog.Info("required checks not green", "failing", g.FailingChecks, "sha", commitSHA)
	}

	g.Behind, g.CompareErr = v.CompareBranches(ctx, pr.BaseRef, commitSHA)
	if g.CompareErr != nil {
		g.Behind = -1 // never reads as up-to-date
	}

	// Match approvals against pr.HeadSHA (from the VCS API) so
	// dismiss_on_new_commit compares against the real PR HEAD, not the SHA
	// the runner happened to check out - which may be a merge commit.
	approvalHeadSHA := pr.HeadSHA
	if approvalHeadSHA == "" {
		approvalHeadSHA = commitSHA
	}
	g.ApprovalsCfg = toApprovalsConfig(shared)
	g.ApprovalPR = approvals.PR{
		Number: prNumber, HeadSHA: approvalHeadSHA, Author: pr.Author, Changed: changed,
	}

	// Gather from every enabled source and union them. pr_review is on by
	// default; pr_comment is opt-in. Deduplication by approver identity
	// happens downstream in Evaluate, so someone who approves via both a
	// review and a comment still counts once.
	var reviewApprovals, commentApprovals []approvals.Approval
	var err error
	if g.ApprovalsCfg.PRReviewEnabled() {
		reviewApprovals, err = v.ListApprovals(ctx, g.ApprovalPR)
		if err != nil {
			// Fail closed: swallowing this left rawApprovals nil, so the
			// gate failed with "no approvals" instead of surfacing the
			// VCS error.
			return nil, fmt.Errorf("list approvals: %w", err)
		}
	}
	if g.ApprovalsCfg.PRCommentEnabled() {
		cfg := commentCfg
		if cfg.Command == "" {
			cfg.Command = g.ApprovalsCfg.CommentCommand()
		}
		commentApprovals, err = v.ListCommentApprovals(ctx, g.ApprovalPR, cfg)
		if err != nil {
			// Fail closed, same as pr_review: a source error blocks rather
			// than silently dropping approvals.
			return nil, fmt.Errorf("list comment approvals: %w", err)
		}
		slog.Debug("comment approvals fetched", "count", len(commentApprovals))
	}
	g.RawApprovals = approvals.MergeApprovals(reviewApprovals, commentApprovals)
	slog.Debug("raw approvals fetched", "count", len(g.RawApprovals), "pr_head_sha", commitSHA)

	// CODEOWNERS is optional: a 404 returns "" with a nil error, so only a
	// real transport error reaches here - and that must not silently pass
	// the codeowners gate.
	coContent, err := v.FetchCodeowners(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch codeowners: %w", err)
	}
	if coContent != "" {
		g.Codeowners = codeowners.Resolve(codeowners.Parse(strings.NewReader(coContent)), changed)
	}
	return g, nil
}

// codeownerApprovers flattens resolved CODEOWNERS into a rule whose
// approvers can be team-expanded. Without it a path owned by @org/team that
// appears in no stack approval rule never gets expanded, and the gate
// always fails.
func codeownerApprovers(resolved map[string][]string) []string {
	if len(resolved) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, owners := range resolved {
		for _, o := range owners {
			seen[o] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for o := range seen {
		out = append(out, o)
	}
	return out
}
