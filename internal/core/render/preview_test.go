package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

var update = flag.Bool("update", false, "update golden files")

func TestPreviewGolden_Basic(t *testing.T) {
	in := PreviewInput{
		Op:          "preview",
		RunNumber:   47,
		CommitSHA:   "abc1234deadbeef",
		DurationSec: 42,
		CIRunURL:    "https://example.com/runs/47",
		Stacks: []summary.StackSummary{
			{
				Project: "api", Stack: "prod", Env: "prod",
				Counts: summary.Counts{Add: 2, Change: 1},
				Status: summary.StatusBlocked, BlockedBy: 482,
				PlanSummary: "+aws:s3:Bucket logs-2026\n~aws:iam:Role app-role",
			},
			{
				Project: "worker", Stack: "prod", Env: "prod",
				Counts:   summary.Counts{Change: 3, Replace: 1},
				Status:   summary.StatusPlanned,
				FullPlan: "pulumi preview output here\nline two",
			},
			{
				Project: "api", Stack: "staging", Env: "staging",
				Counts: summary.Counts{Add: 5},
				Status: summary.StatusPlanned,
			},
			{
				Project: "noop", Stack: "dev", Env: "dev",
				Status: summary.StatusNoOp,
			},
		},
	}
	assertGolden(t, "preview_basic.md", Preview(in))
}

func TestPreviewGolden_NoStacks(t *testing.T) {
	in := PreviewInput{Op: "preview", RunNumber: 1, CommitSHA: "0000000"}
	assertGolden(t, "preview_empty.md", Preview(in))
}

func TestPreviewGolden_AllErrors(t *testing.T) {
	in := PreviewInput{
		Op: "preview", RunNumber: 9, CommitSHA: "deadbee",
		Stacks: []summary.StackSummary{
			{
				Project: "api", Stack: "prod", Env: "prod",
				Status: summary.StatusError,
				Error:  "pulumi preview failed: snake oil",
			},
		},
	}
	assertGolden(t, "preview_error.md", Preview(in))
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run go test -update to create): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch %s\n--- want ---\n%s\n--- got ---\n%s", name, string(want), got)
	}
}

func TestMarkerPresent(t *testing.T) {
	out := Preview(PreviewInput{Op: "preview", RunNumber: 1, CommitSHA: "x"})
	if !strings.HasPrefix(out, Marker) {
		t.Fatalf("output should start with marker %q", Marker)
	}
}

// TestPreviewSizeLimit_DropsFullPlan verifies that when the body exceeds
// GitHub's 65,536-char comment limit, FullPlan is dropped silently --
// reviewers don't read it from the PR comment (CI logs have it), and the
// diff -- which IS what reviewers read -- survives intact. No truncation
// notice should fire because no reviewer-visible content was lost.
func TestPreviewSizeLimit_DropsFullPlan(t *testing.T) {
	// 18 stacks each with a 5KB FullPlan = 90KB, well over the limit.
	bigPlan := strings.Repeat("x", 5*1024)
	stacks := make([]summary.StackSummary, 18)
	for i := range stacks {
		stacks[i] = summary.StackSummary{
			Project:  "infra",
			Stack:    "stack-" + string(rune('a'+i)),
			Env:      "prod",
			Counts:   summary.Counts{Add: 1},
			Status:   summary.StatusPlanned,
			FullPlan: bigPlan,
			PlanDiff: "+ resource foo\n+ resource bar",
		}
	}
	in := PreviewInput{
		Op:        "preview",
		RunNumber: 14,
		CommitSHA: "61964c0",
		CIRunURL:  "https://example.com/runs/14",
		Stacks:    stacks,
	}
	out := Preview(in)

	if len(out) > githubCommentMaxLen {
		t.Fatalf("body %d chars exceeds limit %d", len(out), githubCommentMaxLen)
	}
	if strings.Contains(out, "Full plan output") {
		t.Errorf("expected FullPlan section to be dropped, but it appears in the output")
	}
	// No truncation notice when only FullPlan is dropped -- diffs are intact.
	if strings.Contains(out, "Output trimmed") {
		t.Errorf("did not expect truncation notice when only FullPlan was dropped (diffs are intact)")
	}
	// Per-stack diff is lighter and should survive when only FullPlan is dropped.
	if !strings.Contains(out, "+ resource foo") {
		t.Errorf("expected PlanDiff to survive when only FullPlan needed to be dropped")
	}
}

// TestPreviewSizeLimit_DropsDiffToo verifies the second tier: when even
// dropping FullPlan isn't enough, PlanDiff is dropped as well.
func TestPreviewSizeLimit_DropsDiffToo(t *testing.T) {
	// 40 stacks, each with a 2KB PlanDiff: ~80KB of diff alone, plus
	// table rows and headings push us comfortably past the limit even
	// without FullPlan.
	bigDiff := strings.Repeat("+ resource line\n", 128) // ~2KB
	stacks := make([]summary.StackSummary, 40)
	for i := range stacks {
		stacks[i] = summary.StackSummary{
			Project:  "infra",
			Stack:    "stack-" + strings.Repeat("x", 30) + "-" + string(rune('a'+i%26)),
			Env:      "env",
			Counts:   summary.Counts{Add: 1},
			Status:   summary.StatusPlanned,
			PlanDiff: bigDiff,
		}
	}
	in := PreviewInput{
		Op: "preview", RunNumber: 1, CommitSHA: "abc",
		CIRunURL: "https://example.com/runs/1",
		Stacks:   stacks,
	}
	out := Preview(in)

	if len(out) > githubCommentMaxLen {
		t.Fatalf("body %d chars exceeds limit %d", len(out), githubCommentMaxLen)
	}
	if strings.Contains(out, "<details><summary>Diff</summary>") {
		t.Errorf("expected per-stack Diff section to be dropped at second tier")
	}
	if !strings.Contains(out, "omitted: full plan output, per-stack diff") {
		t.Errorf("expected truncation notice to call out both dropped sections")
	}
}

// TestPreviewSizeLimit_HardTruncate is the safety net: even with all
// per-stack sections dropped, a pathologically long table must still
// produce a body under the limit.
func TestPreviewSizeLimit_HardTruncate(t *testing.T) {
	// 5,000 stacks — each row is short but cumulatively past 65KB.
	stacks := make([]summary.StackSummary, 5000)
	for i := range stacks {
		stacks[i] = summary.StackSummary{
			Project: "p", Stack: "s" + string(rune('a'+i%26)),
			Counts: summary.Counts{Add: 1},
			Status: summary.StatusPlanned,
		}
	}
	out := Preview(PreviewInput{Op: "preview", RunNumber: 1, CommitSHA: "x", Stacks: stacks})
	if len(out) > githubCommentMaxLen {
		t.Fatalf("hard-truncate failed: %d chars > %d", len(out), githubCommentMaxLen)
	}
	if !strings.Contains(out, "hard-truncated") {
		t.Errorf("expected hard-truncation notice; got tail:\n%s", out[max(0, len(out)-300):])
	}
}

// TestPreviewSizeLimit_UnderBudgetUnchanged verifies that bodies safely
// under the limit are emitted verbatim (no truncation notice added).
func TestPreviewSizeLimit_UnderBudgetUnchanged(t *testing.T) {
	in := PreviewInput{
		Op: "preview", RunNumber: 1, CommitSHA: "x",
		Stacks: []summary.StackSummary{
			{
				Project: "p", Stack: "s", Env: "e",
				Counts:   summary.Counts{Add: 1},
				Status:   summary.StatusPlanned,
				FullPlan: "small plan output",
				PlanDiff: "+ one line",
			},
		},
	}
	out := Preview(in)
	if strings.Contains(out, "Output trimmed") {
		t.Errorf("under-budget body should not have a truncation notice")
	}
	if !strings.Contains(out, "small plan output") {
		t.Errorf("under-budget body should contain the full plan verbatim")
	}
}
