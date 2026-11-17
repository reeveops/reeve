package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/approvals"
	"github.com/reeveops/reeve/internal/vcs"
)

// gateVCS is a scriptable stand-in for the gate-input reads.
type gateVCS struct {
	checksGreen   bool
	failingChecks []string
	checksErr     error
	behind        int
	compareErr    error
	reviews       []approvals.Approval
	comments      []approvals.Approval
	codeowners    string

	sawCommentCommand string
	commentCalls      int
}

func (f *gateVCS) ChecksGreen(context.Context, string, vcs.ChecksGreenOpts) (bool, []string, error) {
	if f.checksErr != nil {
		return false, nil, f.checksErr
	}
	return f.checksGreen, f.failingChecks, nil
}
func (f *gateVCS) CompareBranches(context.Context, string, string) (int, error) {
	if f.compareErr != nil {
		return 0, f.compareErr
	}
	return f.behind, nil
}
func (f *gateVCS) ListApprovals(context.Context, approvals.PR) ([]approvals.Approval, error) {
	return f.reviews, nil
}
func (f *gateVCS) ListCommentApprovals(_ context.Context, _ approvals.PR, cfg vcs.CommentApprovalConfig) ([]approvals.Approval, error) {
	f.commentCalls++
	f.sawCommentCommand = cfg.Command
	return f.comments, nil
}
func (f *gateVCS) FetchCodeowners(context.Context) (string, error) { return f.codeowners, nil }
func (f *gateVCS) Name() string                                    { return "fake" }

func testPR() *vcs.PR {
	return &vcs.PR{HeadSHA: "head-sha", BaseRef: "main", Author: "alice"}
}

// TestGatherGateInputsRecordsFailuresFailClosed pins the contract that lets
// apply and explain share this code while disagreeing about error policy:
// an unreadable input is reported via the error field AND left at a value
// that cannot pass a gate. A caller that ignores the error still blocks.
func TestGatherGateInputsRecordsFailuresFailClosed(t *testing.T) {
	v := &gateVCS{
		checksGreen: true, // would pass, but the read fails
		behind:      0,    // would read as up-to-date, but the read fails
		checksErr:   errors.New("checks api down"),
		compareErr:  errors.New("compare api down"),
	}
	gi, err := gatherGateInputs(context.Background(), v, &schemas.Shared{},
		vcs.CommentApprovalConfig{}, 7, testPR(), "sha", nil, vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatalf("read failures must be reported on the struct, not returned: %v", err)
	}
	if gi.ChecksErr == nil || gi.CompareErr == nil {
		t.Fatal("read failures were not recorded")
	}
	if gi.ChecksGreen {
		t.Error("checks_green must be false when it could not be read")
	}
	if gi.UpToDate() {
		t.Error("up-to-date must be false when the comparison could not be read")
	}
}

// TestGatherGateInputsApprovalSources covers the union both callers rely
// on, including the comment-command fallback that was duplicated verbatim.
func TestGatherGateInputsApprovalSources(t *testing.T) {
	yes := true
	shared := &schemas.Shared{
		Approvals: schemas.ApprovalsYAML{
			Sources: []schemas.ApprovalSource{
				{Type: "pr_review", Enabled: &yes},
				{Type: "pr_comment", Enabled: &yes},
			},
		},
	}
	v := &gateVCS{
		reviews:  []approvals.Approval{{Approver: "bob", Source: "pr_review"}},
		comments: []approvals.Approval{{Approver: "carol", Source: "pr_comment"}},
	}
	gi, err := gatherGateInputs(context.Background(), v, shared,
		vcs.CommentApprovalConfig{}, 7, testPR(), "sha", nil, vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gi.RawApprovals) != 2 {
		t.Fatalf("both sources should be unioned, got %+v", gi.RawApprovals)
	}
	// An unset command falls back to the configured default rather than
	// querying for the empty string.
	if v.commentCalls != 1 {
		t.Fatalf("comment source called %d times", v.commentCalls)
	}
	if v.sawCommentCommand == "" {
		t.Error("comment command fallback did not apply")
	}
}

// TestGatherGateInputsUsesPRHeadSHA pins that approvals match against the
// VCS head SHA, not the SHA the runner checked out (which may be a merge
// commit) - the rule dismiss_on_new_commit depends on.
func TestGatherGateInputsUsesPRHeadSHA(t *testing.T) {
	gi, err := gatherGateInputs(context.Background(), &gateVCS{}, &schemas.Shared{},
		vcs.CommentApprovalConfig{}, 7, testPR(), "runner-checkout-sha", nil, vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if gi.ApprovalPR.HeadSHA != "head-sha" {
		t.Fatalf("HeadSHA = %q, want the PR head", gi.ApprovalPR.HeadSHA)
	}

	// With no head SHA from the API, fall back to the evaluated commit.
	pr := testPR()
	pr.HeadSHA = ""
	gi, err = gatherGateInputs(context.Background(), &gateVCS{}, &schemas.Shared{},
		vcs.CommentApprovalConfig{}, 7, pr, "runner-checkout-sha", nil, vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if gi.ApprovalPR.HeadSHA != "runner-checkout-sha" {
		t.Fatalf("fallback HeadSHA = %q", gi.ApprovalPR.HeadSHA)
	}
}

func TestGatherGateInputsRedactsFailingCheckNames(t *testing.T) {
	secret := "ghp_" + strings.Repeat("A", 36)
	v := &gateVCS{failingChecks: []string{"credential-scan/" + secret + " failed"}}
	gi, err := gatherGateInputs(context.Background(), v, &schemas.Shared{},
		vcs.CommentApprovalConfig{}, 7, testPR(), "sha", nil, vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gi.FailingChecks) != 1 || strings.Contains(gi.FailingChecks[0], secret) ||
		!strings.Contains(gi.FailingChecks[0], "credential-scan/[redacted] failed") {
		t.Fatalf("failing checks were not redacted at the run seam: %v", gi.FailingChecks)
	}
	if v.failingChecks[0] != "credential-scan/"+secret+" failed" {
		t.Fatal("redaction must not mutate the VCS adapter's slice")
	}
}

func TestCodeownerApprovers(t *testing.T) {
	got := codeownerApprovers(map[string][]string{
		"a.tf": {"@org/sre", "@alice"},
		"b.tf": {"@org/sre"}, // duplicate must collapse
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 unique owners", got)
	}
	if codeownerApprovers(nil) != nil {
		t.Error("nil input should produce no rule")
	}
}
