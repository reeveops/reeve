package notify

// Channel grouping batches the per-stack drift payloads a single run produces
// into one combined message per group per channel, cutting the notification
// noise when many stacks drift at once. Grouping is a pure dispatch-layer
// concern: it changes only how already-classified payloads are batched for
// delivery, never classification, state, exit_on, or which events fire.
const (
	// GroupingNone keeps the per-stack behavior (one message per drifted
	// stack). It is the default when a channel declares no grouping.
	GroupingNone = "none"
	// GroupingByEnvironment collapses a run's drift alerts into one message
	// per environment, listing that environment's drifted stacks.
	GroupingByEnvironment = "by_environment"
)

// ValidGroupingModes enumerates every accepted `grouping:` value (empty is
// treated as GroupingNone). Config validation rejects anything else.
var ValidGroupingModes = []string{GroupingNone, GroupingByEnvironment}

// IsValidGroupingMode reports whether mode is an accepted grouping value.
// Empty is valid (defaults to none).
func IsValidGroupingMode(mode string) bool {
	if mode == "" {
		return true
	}
	for _, m := range ValidGroupingModes {
		if m == mode {
			return true
		}
	}
	return false
}

// Grouper is optionally implemented by channels that support message grouping.
// Dispatch calls GroupingMode to decide how to batch drift payloads before
// delivering them to that channel; a channel that does not implement Grouper
// (or returns none) always receives the ungrouped per-stack payloads.
type Grouper interface {
	GroupingMode() string
}

// groupableEvent reports whether an event participates in grouping. Only the
// drift alert lifecycle groups; check_failed stays per-stack (each is a
// distinct incident for one stack), and PR-flow events never group.
func groupableEvent(e Event) bool {
	switch e {
	case EventDriftDetected, EventDriftOngoing, EventDriftResolved:
		return true
	}
	return false
}

// GroupPayloads batches drift payloads for one channel according to mode.
//
//   - "" / none: payloads are returned unchanged (per-stack behavior).
//   - by_environment: groupable drift payloads (drift_detected, drift_ongoing,
//     drift_resolved) sharing an (event, env) collapse into a single payload
//     whose Group lists every member and whose GroupKey is the environment.
//     check_failed and non-drift payloads pass through untouched. Relative
//     order is preserved by group first-appearance.
//
// Grouping never adds, drops, or reclassifies events - it only merges deliveries.
func GroupPayloads(payloads []Payload, mode string) []Payload {
	if mode == "" || mode == GroupingNone {
		return payloads
	}
	out := make([]Payload, 0, len(payloads))
	index := map[string]int{} // group key -> position in out
	for _, p := range payloads {
		if p.Drift == nil || !groupableEvent(p.Event) {
			out = append(out, p)
			continue
		}
		key := string(p.Event) + "\x00" + p.Drift.Env
		if i, ok := index[key]; ok {
			out[i].Group = append(out[i].Group, *p.Drift)
			continue
		}
		gp := p
		gp.GroupKey = p.Drift.Env
		gp.Group = []DriftPayload{*p.Drift}
		index[key] = len(out)
		out = append(out, gp)
	}
	return out
}
