package schemas

// ChannelYAML is one generic notification-channel declaration. It is shared by
// notifications.yaml (`channels:`) and drift.yaml (`channels:`). Type chooses the
// adapter; each adapter reads only the fields it cares about. `On` lists the
// events the channel subscribes to (see ValidChannelEvents).
type ChannelYAML struct {
	Type    string   `yaml:"type"`
	Name    string   `yaml:"name,omitempty"`
	Enabled *bool    `yaml:"enabled,omitempty"` // nil == true
	On      []string `yaml:"on,omitempty"`

	// slack
	Channel   string            `yaml:"channel,omitempty"`
	AuthToken string            `yaml:"auth_token,omitempty" expand:"env"` // "${env:SLACK_BOT_TOKEN}"
	Trigger   SlackTrigger      `yaml:"trigger,omitempty"`
	Icons     *SlackIcons       `yaml:"icons,omitempty"`
	Rules     []SlackNotifyRule `yaml:"rules,omitempty"`
	Grouping  string            `yaml:"grouping,omitempty"`

	// webhook. URL and header VALUES are on the env-expansion allow-list
	// (docs show ${env:...} tokens embedded in both); pre-approval previews
	// additionally suppress channel dispatch entirely when the PR modifies
	// notification config - see internal/run/preview.go.
	URL     string            `yaml:"url,omitempty" expand:"env"`
	Headers map[string]string `yaml:"headers,omitempty" expand:"env"`
	Payload DriftPayload      `yaml:"payload,omitempty"`

	// pagerduty
	IntegrationKey string            `yaml:"integration_key,omitempty" expand:"env"`
	SeverityMap    map[string]string `yaml:"severity_map,omitempty"`

	// otel_annotation
	Emitter string `yaml:"emitter,omitempty"`

	// github_issue
	Labels    []string `yaml:"labels,omitempty"`
	Assignees []string `yaml:"assignees,omitempty"`
}

// IsEnabled reports whether the channel should be built. Enabled defaults to
// true when omitted.
func (s ChannelYAML) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// EffectiveName returns Name, falling back to Type.
func (s ChannelYAML) EffectiveName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Type
}

// Channel event names. This is the canonical list validated at load/lint time.
// PR-flow events come from the run pipeline; drift events from the drift
// runner. The strings are shared with internal/notify's Event constants.
const (
	// PR-flow events.
	ChannelEventPlanning = "planning" // preview run started
	ChannelEventPlan     = "plan"     // preview finished, pending approval
	ChannelEventReady    = "ready"    // /reeve ready (or auto_ready)
	ChannelEventApproved = "approved" // preconditions passed, apply imminent
	ChannelEventApplying = "applying" // apply loop started
	ChannelEventApplied  = "applied"  // apply finished successfully
	ChannelEventFailed   = "failed"   // apply errored
	ChannelEventBlocked  = "blocked"  // apply blocked (gates/locks)
	// ChannelEventBreakGlass fires when an emergency-override (break-glass)
	// apply is authorized; the run pipeline emits it in place of approved.
	ChannelEventBreakGlass = "break_glass"

	// Drift events.
	ChannelEventDriftDetected = "drift_detected"
	ChannelEventDriftOngoing  = "drift_ongoing"
	ChannelEventDriftResolved = "drift_resolved"
	ChannelEventCheckFailed   = "check_failed"
	// ChannelEventCheckRecovered fires when a drift check succeeds after one
	// or more failed checks - the all-clear for check_failed. Stateful
	// channels (pagerduty, github_issue) implicitly receive it whenever
	// they subscribe to check_failed, so their incidents/issues resolve.
	ChannelEventCheckRecovered = "check_recovered"
)

// ValidChannelEvents enumerates every valid `on:` entry, in documentation order.
var ValidChannelEvents = []string{
	ChannelEventPlanning,
	ChannelEventPlan,
	ChannelEventReady,
	ChannelEventApproved,
	ChannelEventApplying,
	ChannelEventApplied,
	ChannelEventFailed,
	ChannelEventBlocked,
	ChannelEventBreakGlass,
	ChannelEventDriftDetected,
	ChannelEventDriftOngoing,
	ChannelEventDriftResolved,
	ChannelEventCheckFailed,
	ChannelEventCheckRecovered,
}

// IsValidChannelEvent reports whether name is a known `on:` event.
func IsValidChannelEvent(name string) bool {
	for _, e := range ValidChannelEvents {
		if e == name {
			return true
		}
	}
	return false
}

// ValidDriftChannelEvents is the subset a drift.yaml channel may subscribe to.
// PR-flow events never fire in a drift run, so subscribing to them there is a
// config error (notifications.yaml may still carry both PR-flow and drift
// events; drift.yaml is drift-only).
var ValidDriftChannelEvents = []string{
	ChannelEventDriftDetected,
	ChannelEventDriftOngoing,
	ChannelEventDriftResolved,
	ChannelEventCheckFailed,
	ChannelEventCheckRecovered,
}

// IsValidDriftChannelEvent reports whether name is a valid drift.yaml `on:`
// event.
func IsValidDriftChannelEvent(name string) bool {
	for _, e := range ValidDriftChannelEvents {
		if e == name {
			return true
		}
	}
	return false
}
