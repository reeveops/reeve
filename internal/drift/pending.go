package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/notify"
)

// Drift notification durability
//
// runOne persists the advanced baseline BEFORE the command dispatches
// channel notifications - so a crashed process or a failing channel used
// to lose the event forever: the next run compared against the advanced
// baseline and stayed silent. DispatchDurable closes that window with an
// undelivered-marker protocol:
//
//  1. before dispatching a payload, persist it under
//     drift/pending-events/{project}/{stack}/{event}.json;
//  2. dispatch to every subscribed channel;
//  3. clear the marker only when NO channel reported an error.
//
// The next run loads leftover markers (MergePending) and redelivers them
// ahead of its own payloads. Delivery is therefore at-least-once: a
// partial failure redelivers to every channel, including ones that
// already succeeded. The stateful channels are idempotent (PagerDuty
// dedup keys, github_issue marker upserts); Slack/webhook may repeat a
// message, which beats losing an alert.

// pendingPrefix is the blob namespace for undelivered event markers.
const pendingPrefix = "drift/pending-events"

// PendingStore persists undelivered notification payloads in the bucket.
type PendingStore struct{ Blob blob.Store }

// pendingRecord is the stored form of one undelivered payload.
type pendingRecord struct {
	Event      string              `json:"event"`
	Drift      notify.DriftPayload `json:"drift"`
	RecordedAt time.Time           `json:"recorded_at"`
}

func pendingKey(project, stack, event string) string {
	return fmt.Sprintf("%s/%s/%s/%s.json", pendingPrefix, project, stack, event)
}

// Save persists a payload as an undelivered marker. Payloads without a
// drift body are ignored (the store is drift-specific).
func (p *PendingStore) Save(ctx context.Context, payload notify.Payload) error {
	if payload.Drift == nil {
		return nil
	}
	rec := pendingRecord{Event: string(payload.Event), Drift: *payload.Drift, RecordedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	_, err = p.Blob.Put(ctx, pendingKey(payload.Drift.Project, payload.Drift.Stack, string(payload.Event)), bytes.NewReader(data))
	return err
}

// Clear removes a payload's undelivered marker. Missing is not an error.
func (p *PendingStore) Clear(ctx context.Context, payload notify.Payload) error {
	if payload.Drift == nil {
		return nil
	}
	return p.Blob.Delete(ctx, pendingKey(payload.Drift.Project, payload.Drift.Stack, string(payload.Event)))
}

// List returns every undelivered payload, oldest markers included. Corrupt
// markers are skipped (and reported) rather than wedging every future run.
func (p *PendingStore) List(ctx context.Context) ([]notify.Payload, []error) {
	keys, err := p.Blob.List(ctx, pendingPrefix)
	if err != nil {
		return nil, []error{err}
	}
	var out []notify.Payload
	var errs []error
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		rc, _, err := p.Blob.Get(ctx, k)
		if err != nil {
			errs = append(errs, fmt.Errorf("pending event %s: %w", k, err))
			continue
		}
		var rec pendingRecord
		decErr := json.NewDecoder(rc).Decode(&rec)
		_ = rc.Close()
		if decErr != nil {
			errs = append(errs, fmt.Errorf("pending event %s: %w", k, decErr))
			continue
		}
		d := rec.Drift
		out = append(out, notify.Payload{Event: notify.Event(rec.Event), Drift: &d})
	}
	return out, errs
}

// MergePending prepends previously-undelivered payloads to the current
// run's payloads. A pending payload superseded by a fresh payload for the
// same (project, stack, event) is dropped - the fresh one carries newer
// data and shares the same marker key, so its successful delivery clears
// the old marker too.
func MergePending(pending, current []notify.Payload) []notify.Payload {
	if len(pending) == 0 {
		return current
	}
	seen := make(map[string]bool, len(current))
	for _, p := range current {
		if p.Drift != nil {
			seen[pendingKey(p.Drift.Project, p.Drift.Stack, string(p.Event))] = true
		}
	}
	merged := make([]notify.Payload, 0, len(pending)+len(current))
	for _, p := range pending {
		if p.Drift == nil || seen[pendingKey(p.Drift.Project, p.Drift.Stack, string(p.Event))] {
			continue
		}
		merged = append(merged, p)
	}
	return append(merged, current...)
}

// DispatchDurable delivers payloads with at-least-once semantics (see the
// package comment above). pending may be nil, which degrades to plain
// dispatch. Returned errors include channel delivery errors and marker
// persistence errors; none are fatal to the run.
//
// The whole batch is handed to a SINGLE notify.Dispatch so channel grouping
// (by_environment) can actually batch a run's stacks into one message -
// dispatching one payload at a time would defeat it. Durability is preserved
// at batch granularity: every marker is persisted before delivery, and
// markers are cleared only when the entire batch delivered cleanly. A partial
// failure leaves ALL markers so the next run redelivers to every channel -
// exactly the documented at-least-once behavior (idempotent channels absorb
// the duplicates; a duplicate beats a silently lost alert).
func DispatchDurable(ctx context.Context, channels []notify.Channel, payloads []notify.Payload, pending *PendingStore) []error {
	var errs []error
	if pending != nil {
		for _, p := range payloads {
			if err := pending.Save(ctx, p); err != nil {
				errs = append(errs, fmt.Errorf("persist pending event %s %s: %w", p.Event, refOf(p), err))
			}
		}
	}
	derrs := notify.Dispatch(ctx, channels, payloads)
	errs = append(errs, derrs...)
	if len(derrs) == 0 && pending != nil {
		for _, p := range payloads {
			if err := pending.Clear(ctx, p); err != nil {
				errs = append(errs, fmt.Errorf("clear pending event %s %s: %w", p.Event, refOf(p), err))
			}
		}
	}
	return errs
}

func refOf(p notify.Payload) string {
	if p.Drift == nil {
		return ""
	}
	return p.Drift.Ref()
}
