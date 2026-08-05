package render

import (
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

func TestApplyGolden_Mixed(t *testing.T) {
	in := ApplyInput{
		RunNumber:   99,
		CommitSHA:   "deadbeef1234",
		DurationSec: 120,
		CIRunURL:    "https://example.com/runs/99",
		Stacks: []summary.StackSummary{
			{
				Project: "api", Stack: "prod", Env: "prod",
				Counts:      summary.Counts{Add: 2, Change: 1},
				Status:      summary.StatusPlanned,
				DurationMS:  47_000,
				PlanSummary: "+ s3 bucket\n~ iam role",
			},
			{
				Project: "worker", Stack: "prod", Env: "prod",
				Status:     summary.StatusError,
				DurationMS: 12_000,
				Error:      "aws:rds: Permission denied",
			},
			{
				Project: "api", Stack: "staging", Env: "staging",
				Status: summary.StatusBlocked, BlockedBy: 501,
			},
		},
	}
	assertGolden(t, "apply_mixed.md", Apply(in))
}

func TestApplyGolden_Empty(t *testing.T) {
	assertGolden(t, "apply_empty.md", Apply(ApplyInput{RunNumber: 1, CommitSHA: "x"}))
}

// TestApplySizeLimit_DropsFullPlan verifies Apply enforces the same
// GitHub 65,536-char comment-size cap as Preview.
func TestApplySizeLimit_DropsFullPlan(t *testing.T) {
	bigPlan := strings.Repeat("x", 5*1024)
	stacks := make([]summary.StackSummary, 18)
	for i := range stacks {
		stacks[i] = summary.StackSummary{
			Project:    "infra",
			Stack:      "stack-" + string(rune('a'+i)),
			Env:        "prod",
			Counts:     summary.Counts{Add: 1},
			Status:     summary.StatusPlanned,
			FullPlan:   bigPlan,
			DurationMS: 12_000,
		}
	}
	out := Apply(ApplyInput{
		RunNumber: 5, CommitSHA: "abc",
		CIRunURL: "https://example.com/runs/5",
		Stacks:   stacks,
	})

	if len(out) > githubCommentMaxLen {
		t.Fatalf("apply body %d chars exceeds limit %d", len(out), githubCommentMaxLen)
	}
	if strings.Contains(out, "Full apply output") {
		t.Errorf("expected FullPlan section to be dropped")
	}
	if !strings.Contains(out, "Output trimmed to fit GitHub's 65,536-char comment limit") {
		t.Errorf("expected truncation notice in apply body")
	}
	if !strings.Contains(out, "https://example.com/runs/5") {
		t.Errorf("expected truncation notice to link the CI run URL")
	}
}

func TestApplyPreviewGatesRender(t *testing.T) {
	// Preview comment with a blocked stack carrying a gate trace.
	in := PreviewInput{
		Op: "preview", RunNumber: 2, CommitSHA: "1234567",
		Stacks: []summary.StackSummary{
			{
				Project: "api", Stack: "prod", Env: "prod",
				Counts: summary.Counts{Add: 1},
				Status: summary.StatusPlanned,
				Gates: []summary.GateTrace{
					{Gate: "up_to_date", Outcome: "pass", Reason: "branch up-to-date with base"},
					{Gate: "approvals", Outcome: "fail", Reason: "approvals not satisfied"},
				},
			},
		},
	}
	assertGolden(t, "preview_with_gates.md", Preview(in))
}

func TestApplyBreakGlassSectionIsLoud(t *testing.T) {
	body := Apply(ApplyInput{
		RunNumber: 4, CommitSHA: "abcdef1234",
		Stacks: []summary.StackSummary{{Project: "api", Stack: "prod", Status: summary.StatusPlanned}},
		BreakGlass: &BreakGlassNote{
			Actor:              "alice",
			Justification:      "prod is down\nsecond line",
			AuthorizedVia:      "internal_list",
			Overridden:         []string{"approvals", "not_in_freeze"},
			ConfigModifiedInPR: true,
		},
	})
	for _, want := range []string{
		BreakGlassMarker,
		"[!WARNING]",
		"BREAK-GLASS APPLY",
		"@alice",
		"`internal_list`",
		"`approvals`", "`not_in_freeze`",
		"modified in this same PR",
		"> > prod is down",
		"> > second line",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("break-glass comment missing %q:\n%s", want, body)
		}
	}
}

func TestApplyWithoutBreakGlassHasNoMarker(t *testing.T) {
	body := Apply(ApplyInput{RunNumber: 4, CommitSHA: "abcdef1234"})
	if strings.Contains(body, BreakGlassMarker) {
		t.Fatal("non-break-glass apply must not carry the break-glass marker")
	}
}
