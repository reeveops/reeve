package run

import (
	"context"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
)

// applyNotifyFixture wires a single-stack apply with a timeline_github
// notification channel subscribed to the approved/applying events, so the
// announcements surface in the PR comment the fake VCS records.
func applyNotifyFixture(t *testing.T, approvalsGreen bool) (ApplyInput, *bgVCS) {
	t.Helper()
	blobStore, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &bgEngine{enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}}
	fv := &bgVCS{changed: []string{"projects/api/main.ts"}, headSHA: bgSHA}
	in := plainApplyInput(t, engine, fv, blobStore)
	if !approvalsGreen {
		fv.approvalsList = nil // the approvals gate fails for every stack
	}
	in.Notifications = &schemas.Notifications{
		Header:   schemas.Header{Version: 2, ConfigType: "notifications"},
		Channels: []schemas.ChannelYAML{{Type: "timeline_github", On: []string{"approved", "applying"}}},
	}
	return in, fv
}

// TestGateBlockedApplySendsNoApprovedNotifications pins the ordering fix:
// approved/applying were announced before per-stack gates ran, so a run
// whose gates deny everything told the channel "approved" and "applying"
// while nothing applied. A gate-blocked run sends NEITHER - the gate
// comment is the only notice, and it already names the gate.
func TestGateBlockedApplySendsNoApprovedNotifications(t *testing.T) {
	ctx := context.Background()
	in, fv := applyNotifyFixture(t, false)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("gate-blocked run must be blocked: %+v", out)
	}

	comments := fv.allComments()
	for _, banned := range []string{"✅ **approved**", "🚀 **apply started**"} {
		if strings.Contains(comments, banned) {
			t.Fatalf("gate-blocked run must not announce %q:\n%s", banned, comments)
		}
	}
	if !strings.Contains(comments, "approvals") {
		t.Fatalf("gate-blocked run must still post the gate comment naming the gate:\n%s", comments)
	}
}

// TestApplyingAnnouncedOnlyAfterGatesPass is the control: a run that
// proceeds past gates announces approved + applying exactly once, on the
// PR comment timeline.
func TestApplyingAnnouncedOnlyAfterGatesPass(t *testing.T) {
	ctx := context.Background()
	in, fv := applyNotifyFixture(t, true)

	out, err := Apply(ctx, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Blocked || out.Failed {
		t.Fatalf("run must apply: blocked=%v failed=%v", out.Blocked, out.Failed)
	}

	comments := fv.allComments()
	if !strings.Contains(comments, "✅ **approved**") {
		t.Fatalf("approved announcement missing after gates pass:\n%s", comments)
	}
	if !strings.Contains(comments, "🚀 **apply started**") {
		t.Fatalf("applying announcement missing after gates pass:\n%s", comments)
	}
}
