package approvals

import (
	"strings"
	"testing"
	"time"
)

// evalNow is the fixed evaluation clock for tests. Freshness-free rules
// ignore it entirely.
var evalNow = time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

func TestResolveDefaultAndPatternMerge(t *testing.T) {
	cfg := Config{
		Default: Rules{RequiredApprovals: 1, Approvers: []string{"@org/infra-reviewers"}, Codeowners: true},
		Stacks: []StackRule{
			{
				Pattern: "prod/*",
				Rules:   Rules{RequiredApprovals: 2, Approvers: []string{"@org/sre", "@org/security"}, RequireAllGroups: true},
				Present: map[string]bool{"required_approvals": true, "require_all_groups": true},
			},
			{
				Pattern: "prod/payments",
				Rules:   Rules{Approvers: []string{"@org/payments-leads"}},
				Present: map[string]bool{},
			},
		},
	}
	got := Resolve(cfg, "prod/payments")
	if got.RequiredApprovals != 2 {
		t.Fatalf("expected pattern rule to win required_approvals=2: %+v", got)
	}
	if !got.RequireAllGroups {
		t.Fatalf("require_all_groups should be true")
	}
	// payments-leads unioned in.
	var hasPayments bool
	for _, a := range got.Approvers {
		if a == "@org/payments-leads" {
			hasPayments = true
		}
	}
	if !hasPayments {
		t.Fatalf("approvers missing payments-leads: %v", got.Approvers)
	}
}

func TestEvaluateRequiredApprovals(t *testing.T) {
	rules := Rules{RequiredApprovals: 2, Approvers: []string{"alice", "bob", "carol"}}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave"}
	approvals := []Approval{
		{Approver: "alice", CommitSHA: "sha1"},
		{Approver: "bob", CommitSHA: "sha1"},
	}
	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if !res.Satisfied {
		t.Fatalf("expected satisfied: %+v", res)
	}
}

func TestEvaluateDismissOnNewCommit(t *testing.T) {
	rules := Rules{RequiredApprovals: 1, Approvers: []string{"alice"}, DismissOnNewCommit: true}
	pr := PR{Number: 7, HeadSHA: "sha2"}
	approvals := []Approval{{Approver: "alice", CommitSHA: "sha1", Pinned: true}}
	res := Evaluate(rules, approvals, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("expected unsatisfied after dismissal: %+v", res)
	}
}

func TestEvaluateGroupSemantics(t *testing.T) {
	rules := Rules{
		Approvers:        []string{"org/sre", "org/security"},
		RequireAllGroups: true,
	}
	pr := PR{Number: 7, HeadSHA: "sha1"}
	approvals := []Approval{{Approver: "org/sre"}}
	res := Evaluate(rules, approvals, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("expected unsatisfied (missing security): %+v", res)
	}
	approvals = append(approvals, Approval{Approver: "org/security"})
	res = Evaluate(rules, approvals, pr, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("expected satisfied when both groups approve: %+v", res)
	}
}

func TestEvaluateCodeowners(t *testing.T) {
	rules := Rules{Codeowners: true}
	pr := PR{Number: 7, HeadSHA: "sha1", RepoPrivate: true}
	co := map[string][]string{
		"internal/core/render": {"frontend-team"},
	}
	res := Evaluate(rules, []Approval{{Approver: "frontend-team"}}, pr, co, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("expected codeowners satisfied: %+v", res)
	}
	res = Evaluate(rules, []Approval{{Approver: "someone-else"}}, pr, co, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("expected codeowners unsatisfied: %+v", res)
	}
}

func TestEvaluateSelfApprovalIgnored(t *testing.T) {
	rules := Rules{RequiredApprovals: 1, Approvers: []string{"alice"}}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "alice"}
	approvals := []Approval{{Approver: "alice", SubmittedAt: time.Now()}}
	res := Evaluate(rules, approvals, pr, nil, pr.Author, evalNow)
	if res.Satisfied {
		t.Fatalf("self-approval should not count: %+v", res)
	}
}

func TestEvaluateTeamSlugExpansion(t *testing.T) {
	// Without TeamMembers populated, a rule like `approvers: [org/sre]` only
	// matched if the literal string "org/sre" appeared as an approver -
	// i.e. never. With the expansion map, an actual SRE member's approval
	// satisfies the rule.
	rules := Rules{
		RequiredApprovals: 1,
		Approvers:         []string{"org/sre"},
		TeamMembers:       map[string][]string{"org/sre": {"alice", "bob"}},
	}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave"}
	res := Evaluate(rules, []Approval{{Approver: "alice"}}, pr, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("team expansion should let alice satisfy org/sre: %+v", res)
	}
	// And without expansion, the rule must NOT silently pass on a
	// non-member's approval.
	res = Evaluate(rules, []Approval{{Approver: "carol"}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("non-member must not satisfy team rule: %+v", res)
	}
}

func TestEvaluateRequiredApprovalsCountsTeamMembers(t *testing.T) {
	// required_approvals counts humans, not allow-list entries: two members
	// of a single listed team must satisfy required_approvals=2. Previously
	// each entry could contribute at most one hit, so this could never pass.
	rules := Rules{
		RequiredApprovals: 2,
		Approvers:         []string{"@org/infra-reviewers"},
		TeamMembers:       map[string][]string{"org/infra-reviewers": {"alice", "bob", "carol"}},
	}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave"}
	res := Evaluate(rules, []Approval{{Approver: "alice"}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("one approval must not satisfy required_approvals=2: %+v", res)
	}
	res = Evaluate(rules, []Approval{{Approver: "alice"}, {Approver: "bob"}}, pr, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("two team members must satisfy required_approvals=2: %+v", res)
	}
	// Duplicate approvals from the same person count once.
	res = Evaluate(rules, []Approval{{Approver: "alice"}, {Approver: "alice"}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("duplicate approver must count once: %+v", res)
	}
}

func TestEvaluateInputApprovalsNotMutated(t *testing.T) {
	// The previous Evaluate aliased the input slice via approvals[:0] and
	// patched it back, silently corrupting the caller's slice header.
	rules := Rules{RequiredApprovals: 1, Approvers: []string{"alice"}, DismissOnNewCommit: true}
	pr := PR{Number: 7, HeadSHA: "sha-new"}
	in := []Approval{
		{Approver: "alice", CommitSHA: "sha-old"}, // dismissed
		{Approver: "bob", CommitSHA: "sha-new"},
	}
	snapshot := append([]Approval(nil), in...)
	_ = Evaluate(rules, in, pr, nil, "dave", evalNow)
	for i := range in {
		if in[i] != snapshot[i] {
			t.Fatalf("Evaluate mutated caller's approvals slice at %d: got %+v, want %+v", i, in[i], snapshot[i])
		}
	}
}

// TestEvaluateTeamWithCodeowners verifies that when required_approvals=1,
// codeowners=true, and dismiss_on_new_commit=true, a team member approval on
// the current HEAD SHA satisfies both the required_approvals gate and every
// CODEOWNERS path owned by that team.
func TestEvaluateTeamWithCodeowners(t *testing.T) {
	const headSHA = "abc1234"
	rules := Rules{
		RequiredApprovals:  1,
		Approvers:          []string{"@acme/platform"},
		Codeowners:         true,
		DismissOnNewCommit: true,
		TeamMembers:        map[string][]string{"acme/platform": {"alice", "bob"}},
	}
	pr := PR{Number: 42, HeadSHA: headSHA, Author: "charlie"}
	rawApprovals := []Approval{
		{Approver: "alice", CommitSHA: headSHA, Source: "pr_review", Pinned: true},
	}
	codeowners := map[string][]string{
		"infra/main.tf":  {"@acme/platform"},
		"config/app.yml": {"@acme/platform"},
	}
	res := Evaluate(rules, rawApprovals, pr, codeowners, "charlie", evalNow)
	if !res.Satisfied {
		t.Fatalf("team member approval must satisfy all gates: got=%d needed=%d missing=%v trace=%v",
			res.Got, res.TotalNeeded, res.Missing, res.Trace)
	}

	// Without team expansion, gate must fail closed.
	rulesNoExpansion := rules
	rulesNoExpansion.TeamMembers = nil
	res2 := Evaluate(rulesNoExpansion, rawApprovals, pr, codeowners, "charlie", evalNow)
	if res2.Satisfied {
		t.Fatalf("without team expansion, gate must not pass: %+v", res2)
	}
}

func TestEvaluateNoGatesConfiguredFailsClosed(t *testing.T) {
	// No rules configured must NOT auto-pass: a repo with only a bucket set
	// would otherwise let anyone's /reeve apply run with zero approvals. The
	// safety default injects required_approvals=1.
	res := Evaluate(Rules{}, nil, PR{HeadSHA: "x"}, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("expected no-gates to fail closed: %+v", res)
	}
	// One non-author approval then satisfies the default gate.
	res = Evaluate(Rules{}, []Approval{{Approver: "alice"}}, PR{HeadSHA: "x", RepoPrivate: true}, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("expected one approval to satisfy the default gate: %+v", res)
	}
	// Author's own approval does not count.
	res = Evaluate(Rules{}, []Approval{{Approver: "dave"}}, PR{HeadSHA: "x"}, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("self-approval must not satisfy the default gate: %+v", res)
	}
}

func TestEvaluateRequiredApprovalsEmptyAllowListCountsAnyApprover(t *testing.T) {
	// required_approvals=2 with no approvers list is a valid GitHub-style
	// "require N approvals" gate, not an unsatisfiable one: any 2 distinct
	// non-author approvers satisfy it.
	rules := Rules{RequiredApprovals: 2}
	pr := PR{Number: 1, HeadSHA: "sha1", Author: "dave", RepoPrivate: true}
	res := Evaluate(rules, []Approval{{Approver: "alice"}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("one approval must not satisfy required_approvals=2: %+v", res)
	}
	res = Evaluate(rules, []Approval{{Approver: "alice"}, {Approver: "bob"}}, pr, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("two distinct approvers must satisfy required_approvals=2: %+v", res)
	}
	// Duplicate approvals from one person count once.
	res = Evaluate(rules, []Approval{{Approver: "alice"}, {Approver: "alice"}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("duplicate approver must count once: %+v", res)
	}
}

func TestResolveMoreSpecificPatternWins(t *testing.T) {
	// The more-specific pattern (prod/payments) must override the broader one
	// (prod/*) for numeric fields, regardless of config order. A previously
	// inverted sort let prod/* win, silently lowering the payments gate.
	cfg := Config{
		Stacks: []StackRule{
			{
				Pattern: "prod/*",
				Rules:   Rules{RequiredApprovals: 1},
				Present: map[string]bool{"required_approvals": true},
			},
			{
				Pattern: "prod/payments",
				Rules:   Rules{RequiredApprovals: 3},
				Present: map[string]bool{"required_approvals": true},
			},
		},
	}
	if got := Resolve(cfg, "prod/payments").RequiredApprovals; got != 3 {
		t.Fatalf("more-specific prod/payments must win: got required_approvals=%d, want 3", got)
	}
	// Order in config must not change the outcome.
	cfg.Stacks[0], cfg.Stacks[1] = cfg.Stacks[1], cfg.Stacks[0]
	if got := Resolve(cfg, "prod/payments").RequiredApprovals; got != 3 {
		t.Fatalf("more-specific prod/payments must win regardless of order: got %d, want 3", got)
	}
}

func TestEvaluateFreshness(t *testing.T) {
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: true}
	rules := Rules{RequiredApprovals: 1, Freshness: 24 * time.Hour}

	cases := []struct {
		name      string
		approvals []Approval
		satisfied bool
	}{
		{
			name:      "fresh approval counts",
			approvals: []Approval{{Approver: "alice", SubmittedAt: evalNow.Add(-time.Hour)}},
			satisfied: true,
		},
		{
			name:      "stale approval does not count",
			approvals: []Approval{{Approver: "alice", SubmittedAt: evalNow.Add(-25 * time.Hour)}},
			satisfied: false,
		},
		{
			name:      "untimestamped approval fails closed",
			approvals: []Approval{{Approver: "alice"}},
			satisfied: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(rules, tc.approvals, pr, nil, "dave", evalNow)
			if res.Satisfied != tc.satisfied {
				t.Fatalf("satisfied=%v, want %v: %+v", res.Satisfied, tc.satisfied, res)
			}
		})
	}
}

func TestEvaluateFreshnessStaleSurfacedInTraceAndMissing(t *testing.T) {
	rules := Rules{RequiredApprovals: 1, Freshness: 24 * time.Hour}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave"}
	res := Evaluate(rules, []Approval{{Approver: "alice", SubmittedAt: evalNow.Add(-48 * time.Hour)}}, pr, nil, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("stale approval must not satisfy: %+v", res)
	}
	if !traceContains(res.Trace, "stale alice") {
		t.Fatalf("trace must mention stale approval: %v", res.Trace)
	}
	found := false
	for _, m := range res.Missing {
		if strings.Contains(m, "alice") && strings.Contains(m, "freshness") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing must explain the stale approval: %v", res.Missing)
	}
}

func TestEvaluateZeroFreshnessNoConstraint(t *testing.T) {
	// Freshness=0 means no constraint: ancient and untimestamped approvals
	// both count.
	rules := Rules{RequiredApprovals: 2}
	pr := PR{Number: 7, HeadSHA: "sha1", Author: "dave", RepoPrivate: true}
	res := Evaluate(rules, []Approval{
		{Approver: "alice", SubmittedAt: evalNow.Add(-100 * 24 * time.Hour)},
		{Approver: "bob"},
	}, pr, nil, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("zero freshness must not constrain: %+v", res)
	}
}

func TestEvaluateCodeownersEmailOwnersExcluded(t *testing.T) {
	// A mixed email+login owner list still requires the login owner; the
	// email entry is ignored (reeve cannot match emails to logins).
	rules := Rules{Codeowners: true}
	pr := PR{Number: 7, HeadSHA: "sha1"}
	co := map[string][]string{
		"docs/guide.md": {"docs@example.com", "frontend-team"},
	}
	res := Evaluate(rules, []Approval{{Approver: "someone-else"}}, pr, co, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("login owner still required: %+v", res)
	}
	res = Evaluate(rules, []Approval{{Approver: "frontend-team"}}, pr, co, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("login owner approval must satisfy despite email entry: %+v", res)
	}
	if !traceContains(res.Trace, "docs@example.com is an email address") {
		t.Fatalf("trace must call out the ignored email owner: %v", res.Trace)
	}
}

func TestEvaluateCodeownersEmailOnlyPathDoesNotWedge(t *testing.T) {
	rules := Rules{Codeowners: true}
	pr := PR{Number: 7, HeadSHA: "sha1"}
	co := map[string][]string{
		"docs/guide.md":        {"@docs@example.com"},
		"internal/core/render": {"frontend-team"},
	}
	// The email-only path adds no requirement; the login-owned path still
	// gates, and the overall gate remains satisfiable.
	res := Evaluate(rules, []Approval{{Approver: "frontend-team"}}, pr, co, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("email-only path must not wedge the gate: %+v", res)
	}
	if !traceContains(res.Trace, "all owners are email addresses") {
		t.Fatalf("trace must explain the skipped path: %v", res.Trace)
	}
}

func TestEvaluateCodeownersAllPathsEmailOnlyFallsBackToDefaultGate(t *testing.T) {
	// When every owned path has only email owners, the codeowners gate has
	// nothing enforceable - the safety default (1 approval) applies instead
	// of the gate silently passing or permanently wedging.
	rules := Rules{Codeowners: true}
	pr := PR{Number: 7, HeadSHA: "sha1", RepoPrivate: true}
	co := map[string][]string{"docs/guide.md": {"docs@example.com"}}
	res := Evaluate(rules, nil, pr, co, "dave", evalNow)
	if res.Satisfied {
		t.Fatalf("no approvals must not pass: %+v", res)
	}
	res = Evaluate(rules, []Approval{{Approver: "alice"}}, pr, co, "dave", evalNow)
	if !res.Satisfied {
		t.Fatalf("default gate must be satisfiable: %+v", res)
	}
}

func traceContains(trace []string, substr string) bool {
	for _, tr := range trace {
		if strings.Contains(tr, substr) {
			return true
		}
	}
	return false
}
