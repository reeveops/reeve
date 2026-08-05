package render

import (
	"strings"
	"testing"
)

func TestApplyTimelineAccumulates(t *testing.T) {
	in := TimelineInput{
		RunID:     "apply-118-87dc303",
		RunNumber: 118,
		CommitSHA: "87dc303abc",
		CIRunURL:  "https://ci/run/118",
		Entries: []TimelineEntry{
			{Icon: "🚀", Label: "apply starting"},
			{Icon: "✅", Label: "applied", Detail: "2 stack(s): a/prod, b/prod"},
		},
	}
	got := ApplyTimeline(in)

	if !strings.Contains(got, ApplyTimelineMarker("87dc303")) {
		t.Errorf("missing per-commit marker:\n%s", got)
	}
	if !strings.Contains(got, "run #118") {
		t.Errorf("missing run number:\n%s", got)
	}
	if !strings.Contains(got, "apply starting") || !strings.Contains(got, "applied") {
		t.Errorf("missing timeline entries:\n%s", got)
	}
	if !strings.Contains(got, "2 stack(s): a/prod, b/prod") {
		t.Errorf("missing detail:\n%s", got)
	}
	if !strings.Contains(got, "[run #118](https://ci/run/118)") {
		t.Errorf("run number should hyperlink to the run:\n%s", got)
	}
}

func TestApplyTimelineMarkerUniquePerCommit(t *testing.T) {
	if ApplyTimelineMarker("aaa1234") == ApplyTimelineMarker("bbb5678") {
		t.Fatal("markers must differ per commit")
	}
}

func TestApplyTimelineMarkerStableAcrossRunsOfSameCommit(t *testing.T) {
	// Two runs of the same commit render into the SAME comment (same marker),
	// so a retry or --force re-apply edits the thread rather than posting anew.
	run1 := ApplyTimeline(TimelineInput{RunID: "apply-1-87dc303", RunNumber: 1, CommitSHA: "87dc303abc"})
	run2 := ApplyTimeline(TimelineInput{RunID: "apply-2-87dc303", RunNumber: 2, CommitSHA: "87dc303abc"})
	if !strings.Contains(run1, ApplyTimelineMarker("87dc303")) ||
		!strings.Contains(run2, ApplyTimelineMarker("87dc303")) {
		t.Fatal("both runs of a commit must pin the same per-commit marker")
	}
}

func TestPreviewNoticeBanner(t *testing.T) {
	body := Preview(PreviewInput{
		Op:        "preview",
		RunNumber: 5,
		CommitSHA: "abc1234",
		Notice:    "Commit abc1234 was already applied on run #4.",
	})
	if !strings.Contains(body, "ℹ️") || !strings.Contains(body, "already applied on run #4") {
		t.Errorf("notice banner missing:\n%s", body)
	}
}
