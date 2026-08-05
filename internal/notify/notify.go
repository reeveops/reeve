// Package notify is the shared notification-channel framework. Both producers
// (the drift runner and the PR-flow pipeline) publish events through it; a
// destination implements Channel once and can subscribe to events from either
// producer. Concrete channels live in subpackages under internal/notify/channels
// and self-register via Register in their init(), so a build can compile in
// a subset (see the modularity contract in openspec/specs/architecture).
package notify

import (
	"context"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// Event names a lifecycle event a channel can subscribe to. The string values
// are the `on:` names in config (schemas.ValidChannelEvents).
type Event string

const (
	// PR-flow events (produced by the run pipeline).
	EventPlanning Event = schemas.ChannelEventPlanning // preview run started
	EventPlan     Event = schemas.ChannelEventPlan     // preview finished, pending approval
	EventReady    Event = schemas.ChannelEventReady    // /reeve ready (or auto_ready)
	EventApproved Event = schemas.ChannelEventApproved // preconditions passed, apply imminent
	EventApplying Event = schemas.ChannelEventApplying // apply loop started
	EventApplied  Event = schemas.ChannelEventApplied  // apply finished successfully
	EventFailed   Event = schemas.ChannelEventFailed   // apply errored
	EventBlocked  Event = schemas.ChannelEventBlocked  // apply blocked (gates/locks)
	// EventBreakGlass fires when an emergency-override (break-glass) apply
	// is authorized; run.Apply emits it in place of EventApproved.
	EventBreakGlass Event = schemas.ChannelEventBreakGlass

	// Drift events (produced by the drift runner).
	EventDriftDetected Event = schemas.ChannelEventDriftDetected
	EventDriftOngoing  Event = schemas.ChannelEventDriftOngoing
	EventDriftResolved Event = schemas.ChannelEventDriftResolved
	EventCheckFailed   Event = schemas.ChannelEventCheckFailed
	// EventCheckRecovered is the all-clear for EventCheckFailed: the first
	// successful check after one or more failed ones. Stateful channels use
	// it to resolve the incident/issue a check_failed opened.
	EventCheckRecovered Event = schemas.ChannelEventCheckRecovered
)

// PREvents lists the core PR-flow lifecycle events in order. The legacy
// Slack trigger-onward default subscription derives from this list, so it
// deliberately EXCLUDES the timeline-only additions (planning, break_glass):
// adding them here would silently widen existing channels' subscriptions.
func PREvents() []Event {
	return []Event{EventPlan, EventReady, EventApproved, EventApplying, EventApplied, EventFailed, EventBlocked}
}

// TimelinePREvents lists every PR-flow event the deployment timeline
// renders, in lifecycle order: the core set plus preview-started and the
// reserved break-glass event. Timeline channels subscribe to this set by
// default.
func TimelinePREvents() []Event {
	return append(append([]Event{EventPlanning}, PREvents()...), EventBreakGlass)
}

// DriftEvents lists every drift event.
func DriftEvents() []Event {
	return []Event{EventDriftDetected, EventDriftOngoing, EventDriftResolved, EventCheckFailed, EventCheckRecovered}
}

// WithImpliedEvents returns events, additionally subscribing stateful
// channels to the all-clear counterpart of any alerting event they chose:
// a channel that opens an incident/issue on check_failed must also hear
// check_recovered, or the incident survives forever once the check heals.
// Used by the pagerduty and github_issue constructors.
func WithImpliedEvents(events []Event) []Event {
	hasFailed, hasRecovered := false, false
	for _, e := range events {
		switch e {
		case EventCheckFailed:
			hasFailed = true
		case EventCheckRecovered:
			hasRecovered = true
		}
	}
	if hasFailed && !hasRecovered {
		events = append(events, EventCheckRecovered)
	}
	return events
}

// ParseEvents converts `on:` strings into Events, dropping unknown names.
// Unknown names are rejected earlier, at config load/lint time.
func ParseEvents(on []string) []Event {
	out := make([]Event, 0, len(on))
	for _, s := range on {
		if schemas.IsValidChannelEvent(s) {
			out = append(out, Event(s))
		}
	}
	return out
}

// Payload is what every channel receives per delivery. Exactly one of Drift or
// PR is non-nil, matching the event's producer.
type Payload struct {
	Event Event
	Drift *DriftPayload
	PR    *PRPayload
	// Group, when non-empty, lists every drift member batched into this single
	// delivery by channel grouping (see GroupPayloads). It always includes the
	// member Drift points at, so a grouping-unaware channel that only reads
	// Drift still renders a valid (if un-batched) message. GroupKey identifies
	// the group (the environment for by_environment) for stateful channels that
	// keep one record per group. Empty Group == an ordinary per-stack delivery.
	Group    []DriftPayload
	GroupKey string
}

// DriftPayload is one stack's drift-check outcome, flattened from
// drift.RunOutput so channels do not depend on the drift package.
type DriftPayload struct {
	Project     string
	Stack       string
	Env         string
	Outcome     string // no_drift | drift_detected | error | skipped_fresh
	Add         int
	Change      int
	Delete      int
	Replace     int
	PlanSummary string
	Fingerprint string
	Error       string
	RunID       string
}

// Ref returns "project/stack".
func (d DriftPayload) Ref() string { return d.Project + "/" + d.Stack }

// PRPayload is the PR-flow event context, flattened from the run pipeline.
type PRPayload struct {
	PR                int
	CommitSHA         string
	RunURL            string
	Title             string
	Author            string
	RepoFull          string // "owner/repo"
	RequiredApprovers []string
	Stacks            []StackResult
}

// StackResult is one stack's summary inside a PR-flow payload.
type StackResult struct {
	Project string
	Stack   string
	Env     string
	Status  string // planned | noop | blocked | error
	Add     int
	Change  int
	Delete  int
	Replace int
}

// Total is the change-count sum, used for no-op detection.
func (s StackResult) Total() int { return s.Add + s.Change + s.Delete + s.Replace }

// Channel delivers events. Implementations are expected to be reentrant-safe,
// side-effect-only, and to honor ctx cancellation (Dispatch enforces a
// per-delivery timeout through ctx).
type Channel interface {
	Name() string
	// Subscribes returns the event types this channel wants to receive.
	Subscribes() []Event
	// Deliver publishes one event. Return error only for unrecoverable
	// failures; channels swallow transient errors internally (Deliver-level
	// retries for HTTP channels live in PostJSON).
	Deliver(ctx context.Context, p Payload) error
}
