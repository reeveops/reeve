package render

import (
	"fmt"
	"strings"

	"github.com/reeveops/reeve/internal/core/summary"
)

// ExplainMarker keys the explain comment per commit: repeat invocations at
// the same SHA edit one comment in place, a new commit gets a new comment.
// Same consolidation rule as the apply timeline.
func ExplainMarker(shortSHA string) string {
	return fmt.Sprintf("<!-- reeve:explain:%s -->", shortSHA)
}

// ExplainStack is one stack's answer to "why?": the approval rules as
// resolved, the lock as read, and a report-only gate evaluation.
type ExplainStack struct {
	Ref string

	// Approval rules as resolved for this stack.
	RequiredApprovals  int
	RequireAllGroups   bool
	Codeowners         bool
	DismissOnNewCommit bool
	Approvers          []string
	// Live approval standing.
	ApprovalsGot     int
	ApprovalsNeeded  int
	ApprovalsMissing []string

	// Lock state, from a plain read. Never an acquisition.
	LockStatus     string // free | held | expired
	LockHolderPR   int
	LockHolderRun  string
	LockAcquiredAt string
	LockExpiresAt  string
	LockQueuePRs   []int

	Gates []summary.GateTrace
}

// ExplainInput feeds Explain.
type ExplainInput struct {
	CommitSHA string
	RunURL    string
	Stacks    []ExplainStack
}

// Explain renders the /reeve explain comment: per stack, the resolved
// approval rules, the lock state, and the full gate trace. Read-only by
// contract - everything here was gathered without acting.
func Explain(in ExplainInput) string {
	var b strings.Builder
	b.WriteString(ExplainMarker(shortSHA(in.CommitSHA)) + "\n")
	if in.RunURL != "" {
		fmt.Fprintf(&b, "### 🔎 reeve · explain · [run](%s) · commit %s\n\n", in.RunURL, shortSHA(in.CommitSHA))
	} else {
		fmt.Fprintf(&b, "### 🔎 reeve · explain · commit %s\n\n", shortSHA(in.CommitSHA))
	}
	b.WriteString("Report-only: nothing below ran an engine, took a lock, or wrote state.\n\n")

	for _, s := range in.Stacks {
		fmt.Fprintf(&b, "---\n\n#### %s\n\n", s.Ref)

		b.WriteString("**Approval rules**\n\n")
		fmt.Fprintf(&b, "- required approvals: %d (have %d of %d)\n", s.RequiredApprovals, s.ApprovalsGot, s.ApprovalsNeeded)
		if len(s.Approvers) > 0 {
			fmt.Fprintf(&b, "- approvers: %s\n", strings.Join(s.Approvers, ", "))
		}
		if len(s.ApprovalsMissing) > 0 {
			fmt.Fprintf(&b, "- missing: %s\n", strings.Join(s.ApprovalsMissing, ", "))
		}
		fmt.Fprintf(&b, "- require_all_groups: %t · codeowners: %t · dismiss_on_new_commit: %t\n\n",
			s.RequireAllGroups, s.Codeowners, s.DismissOnNewCommit)

		b.WriteString("**Lock**\n\n")
		switch {
		case s.LockHolderPR > 0:
			fmt.Fprintf(&b, "- %s by PR #%d (run %s, acquired %s, expires %s)\n",
				s.LockStatus, s.LockHolderPR, orDash(s.LockHolderRun), orDash(s.LockAcquiredAt), orDash(s.LockExpiresAt))
		default:
			fmt.Fprintf(&b, "- %s\n", s.LockStatus)
		}
		if len(s.LockQueuePRs) > 0 {
			refs := make([]string, 0, len(s.LockQueuePRs))
			for _, pr := range s.LockQueuePRs {
				refs = append(refs, fmt.Sprintf("#%d", pr))
			}
			fmt.Fprintf(&b, "- queue: %s\n", strings.Join(refs, " → "))
		}
		b.WriteString("\n")

		if len(s.Gates) > 0 {
			fmt.Fprintf(&b, "🔐 %s apply gates:\n", s.Ref)
			for _, g := range s.Gates {
				fmt.Fprintf(&b, "  %s %s: %s\n", gateIcon(g.Outcome), g.Gate, g.Reason)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n\n_`/reeve explain [project/stack]` re-renders this comment. Gates are evaluated now; a later push or approval changes the answer._\n")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
