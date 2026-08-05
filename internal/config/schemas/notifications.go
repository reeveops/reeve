package schemas

// Notifications is .reeve/notifications.yaml: a generic `channels:` list
// (type + settings + `on:` subscriptions).
//
// The original single `slack:` block is no longer loaded — the field exists
// only so the loader can reject it with a pointer at `reeve migrate-config`,
// which converts it to a channels: entry.
type Notifications struct {
	Header   `yaml:",inline"`
	Slack    *SlackConfig  `yaml:"slack,omitempty"`
	Channels []ChannelYAML `yaml:"channels,omitempty"`
}

// SlackTrigger controls when the first Slack message is created.
type SlackTrigger string

const (
	// SlackTriggerApply creates the message only when apply is invoked (default).
	SlackTriggerApply SlackTrigger = "apply"
	// SlackTriggerPlan creates the message when a plan finishes (status: pending approval).
	SlackTriggerPlan SlackTrigger = "plan"
	// SlackTriggerReady creates the message only when /reeve ready is run.
	SlackTriggerReady SlackTrigger = "ready"
)

// SlackEvent represents a named lifecycle event that can emit a Slack notification.
type SlackEvent string

const (
	SlackEventPlan     SlackEvent = "plan"
	SlackEventReady    SlackEvent = "ready"
	SlackEventApproved SlackEvent = "approved"
	SlackEventApplying SlackEvent = "applying"
	SlackEventApplied  SlackEvent = "applied"
	SlackEventFailed   SlackEvent = "failed"
	SlackEventBlocked  SlackEvent = "blocked"
)

// slackEventOrder defines the natural lifecycle order for default filtering.
var slackEventOrder = []SlackEvent{
	SlackEventPlan,
	SlackEventReady,
	SlackEventApproved,
	SlackEventApplying,
	SlackEventApplied,
	SlackEventFailed,
	SlackEventBlocked,
}

// SlackEventEnabled returns true if the given event should emit a Slack notification.
// When Events is empty, all events at or after the trigger are enabled by default.
func SlackEventEnabled(cfg *SlackConfig, event SlackEvent) bool {
	if cfg == nil {
		return false
	}
	if len(cfg.Events) > 0 {
		for _, e := range cfg.Events {
			if e == event {
				return true
			}
		}
		return false
	}
	// Default: all events from trigger onward.
	triggerEvent := SlackEvent(cfg.Trigger)
	if triggerEvent == "" {
		triggerEvent = SlackEvent(SlackTriggerApply)
	}
	triggerIdx := -1
	eventIdx := -1
	for i, e := range slackEventOrder {
		if e == triggerEvent {
			triggerIdx = i
		}
		if e == event {
			eventIdx = i
		}
	}
	if triggerIdx < 0 || eventIdx < 0 {
		return true
	}
	return eventIdx >= triggerIdx
}

// SlackIcons overrides the default emoji used in messages.
// All fields are optional -- unset fields use the built-in defaults.
type SlackIcons struct {
	// Engine is the icon shown next to the repo/project name.
	// Default: ":building_construction:"
	Engine string `yaml:"engine,omitempty"`
	// Runner is the icon for the CI runner / GitHub Actions.
	// Default: ":runner:"
	Runner string `yaml:"runner,omitempty"`
	// Author is the icon next to the PR author field.
	// Default: ":writing_hand:"
	Author string `yaml:"author,omitempty"`
	// Approver is the icon next to the approvers field.
	// Default: ":approved_stamp:"
	Approver string `yaml:"approver,omitempty"`
}

// SlackConfig wires the Slack notification backend.
type SlackConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Channel   string `yaml:"channel"`
	AuthToken string `yaml:"auth_token"` // "${env:SLACK_BOT_TOKEN}"
	// Trigger controls when the initial Slack message is created.
	// Subsequent events always update the existing message in place.
	// Default: "apply"
	Trigger SlackTrigger `yaml:"trigger,omitempty"`
	// Events lists which lifecycle events emit a Slack notification.
	// When empty, all events at or after the trigger fire (default behavior).
	// Valid values: plan, ready, approved, applying, applied, failed, blocked.
	Events []SlackEvent      `yaml:"events,omitempty"`
	Icons  *SlackIcons       `yaml:"icons,omitempty"`
	Rules  []SlackNotifyRule `yaml:"rules,omitempty"`
}

// SlackNotifyRule gates which stacks flow into Slack (e.g. environments: [prod]).
type SlackNotifyRule struct {
	Environments []string `yaml:"environments,omitempty"`
	Stacks       []string `yaml:"stacks,omitempty"` // glob patterns
}
