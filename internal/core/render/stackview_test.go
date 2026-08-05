package render

import (
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

func stackViewStacks() []summary.StackSummary {
	return []summary.StackSummary{
		{Project: "a", Stack: "prod", Env: "prod", Status: summary.StatusPlanned, Counts: summary.Counts{Add: 1}},
		{Project: "b", Stack: "prod", Env: "prod", Status: summary.StatusNoOp},
		{Project: "c", Stack: "prod", Env: "prod", Status: summary.StatusNoOp},
	}
}

func TestPreviewStackViewAllShowsNoOps(t *testing.T) {
	body := Preview(PreviewInput{Op: "preview", Stacks: stackViewStacks(), StackView: StackViewAll})
	for _, ref := range []string{"a/prod", "b/prod", "c/prod"} {
		if !strings.Contains(body, ref) {
			t.Errorf("view=all should list %s:\n%s", ref, body)
		}
	}
}

func TestPreviewStackViewChangedHidesNoOps(t *testing.T) {
	body := Preview(PreviewInput{Op: "preview", Stacks: stackViewStacks(), StackView: StackViewChanged})
	if !strings.Contains(body, "a/prod") {
		t.Errorf("changed view must keep changed stack a/prod:\n%s", body)
	}
	if strings.Contains(body, "b/prod") || strings.Contains(body, "c/prod") {
		t.Errorf("changed view must hide no-op stacks:\n%s", body)
	}
}

func TestPreviewStackViewDefaultIsAll(t *testing.T) {
	// Empty StackView == "all".
	body := Preview(PreviewInput{Op: "preview", Stacks: stackViewStacks()})
	if !strings.Contains(body, "b/prod") {
		t.Errorf("default view should show no-ops:\n%s", body)
	}
}

func TestApplyStackViewChangedHidesNoOps(t *testing.T) {
	body := Apply(ApplyInput{Stacks: stackViewStacks(), StackView: StackViewChanged})
	if !strings.Contains(body, "a/prod") {
		t.Errorf("apply changed view must keep a/prod:\n%s", body)
	}
	if strings.Contains(body, "b/prod") {
		t.Errorf("apply changed view must hide no-ops:\n%s", body)
	}
}
