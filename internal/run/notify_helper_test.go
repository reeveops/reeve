package run

import (
	"context"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/notify"
)

type captureChannel struct {
	events   []notify.Event
	payloads []notify.Payload
}

func (c *captureChannel) Name() string               { return "capture" }
func (c *captureChannel) Subscribes() []notify.Event { return c.events }
func (c *captureChannel) Deliver(_ context.Context, p notify.Payload) error {
	c.payloads = append(c.payloads, p)
	return nil
}

func TestNotifyPREventBuildsPayload(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "org/repo")
	channel := &captureChannel{events: notify.PREvents()}
	err := NotifyPREvent(context.Background(), []notify.Channel{channel}, notify.EventPlan, PRNotifyInput{
		PR: 9, CommitSHA: "abc", RunURL: "https://ci", PRTitle: "t", PRAuthor: "a",
		RequiredApprovers: []string{"lead"},
		Stacks: []summary.StackSummary{
			{Project: "app", Stack: "prod", Env: "prod", Status: summary.StatusPlanned,
				Counts: summary.Counts{Add: 1, Change: 2, Delete: 3, Replace: 4}},
			{Project: "app", Stack: "dev", Env: "dev", Status: summary.StatusError},
		},
	})
	if err != nil {
		t.Fatalf("NotifyPREvent: %v", err)
	}
	if len(channel.payloads) != 1 {
		t.Fatalf("payloads: %d", len(channel.payloads))
	}
	p := channel.payloads[0]
	if p.Event != notify.EventPlan || p.Drift != nil || p.PR == nil {
		t.Fatalf("payload: %+v", p)
	}
	pr := p.PR
	if pr.PR != 9 || pr.RepoFull != "org/repo" || pr.Title != "t" || pr.Author != "a" {
		t.Fatalf("pr payload: %+v", pr)
	}
	if len(pr.Stacks) != 2 || pr.Stacks[0].Status != "planned" || pr.Stacks[1].Status != "error" {
		t.Fatalf("stacks: %+v", pr.Stacks)
	}
	if pr.Stacks[0].Add != 1 || pr.Stacks[0].Replace != 4 {
		t.Fatalf("counts: %+v", pr.Stacks[0])
	}
}

func TestNotifyPREventRespectsSubscriptions(t *testing.T) {
	channel := &captureChannel{events: []notify.Event{notify.EventApplied}}
	if err := NotifyPREvent(context.Background(), []notify.Channel{channel}, notify.EventPlan, PRNotifyInput{PR: 1}); err != nil {
		t.Fatal(err)
	}
	if len(channel.payloads) != 0 {
		t.Fatalf("unsubscribed event delivered: %+v", channel.payloads)
	}
}

func TestNotifyPREventPlanningReachesTimelineNotLegacyDefaults(t *testing.T) {
	// planning (preview started) is a timeline event: channels on the timeline
	// default receive it; channels on the legacy PR-lifecycle default do not,
	// so existing subscriptions keep their exact behavior.
	timeline := &captureChannel{events: notify.TimelinePREvents()}
	legacy := &captureChannel{events: notify.PREvents()}
	err := NotifyPREvent(context.Background(), []notify.Channel{timeline, legacy}, notify.EventPlanning,
		PRNotifyInput{PR: 3, CommitSHA: "abc", RunURL: "https://ci/run/1"})
	if err != nil {
		t.Fatalf("NotifyPREvent: %v", err)
	}
	if len(timeline.payloads) != 1 || timeline.payloads[0].Event != notify.EventPlanning {
		t.Fatalf("timeline subscriber: %+v", timeline.payloads)
	}
	if len(legacy.payloads) != 0 {
		t.Fatalf("legacy default must not widen: %+v", legacy.payloads)
	}
}

func TestApplyOutcomeEvent(t *testing.T) {
	errStack := []summary.StackSummary{{Status: summary.StatusError}}
	okStack := []summary.StackSummary{{Status: summary.StatusPlanned}}
	if got := ApplyOutcomeEvent(errStack, true); got != notify.EventFailed {
		t.Fatalf("errors must win: %v", got)
	}
	if got := ApplyOutcomeEvent(okStack, true); got != notify.EventBlocked {
		t.Fatalf("blocked: %v", got)
	}
	if got := ApplyOutcomeEvent(okStack, false); got != notify.EventApplied {
		t.Fatalf("applied: %v", got)
	}
}
