package run

import (
	"context"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/render"
)

// TestApplyTimelineConsolidatesPerCommit: two apply runs of the SAME commit
// (e.g. an apply then a --force re-apply) must share ONE comment thread pinned
// by the per-commit marker, and the latest render must carry both runs'
// entries - the persisted state accumulates instead of each run clobbering or
// posting a fresh comment.
func TestApplyTimelineConsolidatesPerCommit(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fv := &bgVCS{}
	const sha = "abc1234def5678"

	tl1 := newApplyTimeline(fv, fs, 7, "apply-1-"+shortSHA(sha), 1, sha, "https://ci/run/1")
	tl1.add(ctx, "🚀", "apply starting", "")
	tl1.add(ctx, "✅", "applied", "2 stack(s): api/prod, worker/prod")

	tl2 := newApplyTimeline(fv, fs, 7, "apply-2-"+shortSHA(sha), 2, sha, "https://ci/run/2")
	tl2.add(ctx, "🚀", "apply starting", "")
	tl2.add(ctx, "✅", "applied", "1 stack(s): api/prod")

	if len(fv.comments) != 1 {
		t.Fatalf("want ONE comment thread for the commit, got %d markers", len(fv.comments))
	}
	marker := render.ApplyTimelineMarker(shortSHA(sha))
	bodies, ok := fv.comments[marker]
	if !ok {
		t.Fatalf("comment not pinned by per-commit marker %q", marker)
	}

	last := bodies[len(bodies)-1]
	for _, want := range []string{
		"2 stack(s): api/prod, worker/prod", // from run 1
		"1 stack(s): api/prod",              // from run 2
	} {
		if !strings.Contains(last, want) {
			t.Errorf("consolidated thread missing %q:\n%s", want, last)
		}
	}
	// Header shows the latest run to touch the commit, linked to its run.
	if !strings.Contains(last, "[run #2](https://ci/run/2)") {
		t.Errorf("header should link the latest run:\n%s", last)
	}
}

// TestApplyTimelineInMemoryWithoutBlob: with no blob store (local run, no
// bucket) the timeline still accumulates this run's entries in one comment.
func TestApplyTimelineInMemoryWithoutBlob(t *testing.T) {
	ctx := context.Background()
	fv := &bgVCS{}
	const sha = "deadbeef1234"

	tl := newApplyTimeline(fv, nil, 3, "apply-1-"+shortSHA(sha), 1, sha, "")
	tl.add(ctx, "🚀", "apply starting", "")
	tl.add(ctx, "✅", "applied", "1 stack(s): api/prod")

	marker := render.ApplyTimelineMarker(shortSHA(sha))
	bodies := fv.comments[marker]
	if len(bodies) == 0 {
		t.Fatalf("no comment posted under per-commit marker %q", marker)
	}
	last := bodies[len(bodies)-1]
	if !strings.Contains(last, "apply starting") || !strings.Contains(last, "1 stack(s): api/prod") {
		t.Errorf("in-memory timeline missing entries:\n%s", last)
	}
}
