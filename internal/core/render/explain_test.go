package render

import (
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/core/summary"
)

func TestExplainGolden_Basic(t *testing.T) {
	in := ExplainInput{
		CommitSHA: "3f9a1c2deadbeef",
		RunURL:    "https://example.com/runs/12",
		Stacks: []ExplainStack{
			{
				Ref:               "api/prod",
				RequiredApprovals: 2, Approvers: []string{"@dana", "@sam"},
				ApprovalsGot: 1, ApprovalsNeeded: 2, ApprovalsMissing: []string{"@sam"},
				DismissOnNewCommit: true,
				LockStatus:         "held", LockHolderPR: 481, LockHolderRun: "4817",
				LockAcquiredAt: "2026-08-06T12:10:02Z", LockExpiresAt: "2026-08-06T16:10:02Z",
				LockQueuePRs: []int{482, 490},
				Gates: []summary.GateTrace{
					{Gate: "up_to_date", Outcome: "pass", Reason: "branch up-to-date with base"},
					{Gate: "approvals", Outcome: "fail", Reason: "approvals not satisfied"},
					{Gate: "lock_acquirable", Outcome: "fail", Reason: "blocked by lock held by PR #481"},
				},
			},
			{
				Ref:               "api/staging",
				RequiredApprovals: 1, Approvers: []string{"@dana", "@sam"},
				ApprovalsGot: 1, ApprovalsNeeded: 1,
				LockStatus: "free",
				Gates: []summary.GateTrace{
					{Gate: "approvals", Outcome: "pass", Reason: "approvals satisfied"},
					{Gate: "lock_acquirable", Outcome: "pass", Reason: "lock is acquirable"},
				},
			},
		},
	}
	got := Explain(in)
	assertGolden(t, "explain_basic.md", got)

	if !strings.Contains(got, ExplainMarker("3f9a1c2")) {
		t.Fatal("explain comment must carry its per-commit marker")
	}
	if !strings.Contains(got, "Report-only") {
		t.Fatal("explain comment must state it is report-only")
	}
}
