package approvals

import (
	"strings"
	"testing"
)

// On a public repo, a bare numeric gate (no approvers list, no CODEOWNERS)
// cannot be satisfied by unlisted reviews: anyone can submit an approving
// review, so it is not a real gate. The default must fail closed with an
// actionable message.
func TestEvaluate_PublicRepoUnlistedApprovalsBlocked(t *testing.T) {
	rules := Rules{RequiredApprovals: 1} // no Approvers, no Codeowners
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: false}
	approvals := []Approval{{Approver: "random-user", CommitSHA: "sha1"}}

	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if res.Satisfied {
		t.Fatalf("public repo bare-numeric gate must not be satisfied by an unlisted review: %+v", res)
	}
	joined := strings.Join(res.Missing, " | ") + " " + strings.Join(res.Trace, " ")
	if !strings.Contains(joined, "allow_unlisted_approvals_on_public") {
		t.Fatalf("message should name the opt-in flag; got missing=%v trace=%v", res.Missing, res.Trace)
	}
}

// The same repo with the explicit opt-in counts unlisted reviews again.
func TestEvaluate_PublicRepoOptInCountsUnlisted(t *testing.T) {
	rules := Rules{RequiredApprovals: 1, AllowUnlistedApprovalsOnPublic: true}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: false}
	approvals := []Approval{{Approver: "random-user", CommitSHA: "sha1"}}

	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if !res.Satisfied {
		t.Fatalf("opt-in should count the unlisted review: %+v", res)
	}
}

// A private repo is unaffected: the reviewer set is already the collaborator
// set, so a bare numeric gate counts a distinct review as before.
func TestEvaluate_PrivateRepoUnlistedApprovalsCounted(t *testing.T) {
	rules := Rules{RequiredApprovals: 1}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: true}
	approvals := []Approval{{Approver: "some-collaborator", CommitSHA: "sha1"}}

	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if !res.Satisfied {
		t.Fatalf("private repo bare-numeric gate should count a distinct review: %+v", res)
	}
}

// A public repo with a configured approvers list is a real gate and is not
// blocked by the public-repo guard (which only fires on the no-allow-list
// path).
func TestEvaluate_PublicRepoWithApproversListUnaffected(t *testing.T) {
	rules := Rules{RequiredApprovals: 1, Approvers: []string{"alice"}}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: false}
	approvals := []Approval{{Approver: "alice", CommitSHA: "sha1"}}

	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if !res.Satisfied {
		t.Fatalf("public repo with an approvers list should be satisfiable: %+v", res)
	}
}
