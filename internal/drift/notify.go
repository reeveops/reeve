package drift

import "github.com/FynxLabs/reeve/internal/notify"

// NotifyPayloads flattens a RunOutput into notification payloads, one per
// item with a non-silent NOTIFICATION event (Item.NotifyEvent - the
// classification event after flap damping), plus a check_recovered payload
// for every item whose check succeeded after previous failures. This is the
// drift producer's adapter onto the shared channel framework.
func NotifyPayloads(out *RunOutput) []notify.Payload {
	if out == nil {
		return nil
	}
	payloads := make([]notify.Payload, 0, len(out.Items))
	for _, it := range out.Items {
		// Recovery first, so a channel resolving the check-failed incident
		// does it before any fresh drift alert for the same stack.
		if it.CheckRecovered {
			payloads = append(payloads, payloadFor(out, it, notify.EventCheckRecovered))
		}
		if it.NotifyEvent == EventNone {
			continue
		}
		payloads = append(payloads, payloadFor(out, it, notify.Event(it.NotifyEvent)))
	}
	return payloads
}

func payloadFor(out *RunOutput, it Item, ev notify.Event) notify.Payload {
	return notify.Payload{
		Event: ev,
		Drift: &notify.DriftPayload{
			Project:     it.Project,
			Stack:       it.Stack,
			Env:         it.Env,
			Outcome:     string(it.Outcome),
			Add:         it.Counts.Counts.Add,
			Change:      it.Counts.Counts.Change,
			Delete:      it.Counts.Counts.Delete,
			Replace:     it.Counts.Counts.Replace,
			PlanSummary: it.Counts.PlanSummary,
			Fingerprint: it.Fingerprint,
			Error:       it.Error,
			RunID:       out.RunID,
		},
	}
}
