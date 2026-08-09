// Package otel delivers events as annotation events to the
// observability.annotations emitters (Grafana / Datadog / Dash0 / webhook).
package otel

import (
	"context"
	"log/slog"
	"time"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/notify"
	"github.com/reeveops/reeve/internal/observability/annotations"
)

func init() {
	notify.Register("otel_annotation", New)
}

// Channel fans events out to a list of annotation emitters.
type Channel struct {
	name     string
	events   []notify.Event
	emitters []annotations.Emitter
}

// New is the registered constructor. With no emitters configured the channel
// is skipped, matching the previous factory behavior.
func New(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	if len(deps.Emitters) == 0 {
		return nil, nil
	}
	return &Channel{
		name:     cfg.EffectiveName(),
		events:   notify.ParseEvents(cfg.On),
		emitters: deps.Emitters,
	}, nil
}

func (s *Channel) Name() string               { return s.name }
func (s *Channel) Subscribes() []notify.Event { return s.events }

func (s *Channel) Deliver(ctx context.Context, p notify.Payload) error {
	switch {
	case p.Drift != nil:
		t := driftEventType(p.Event)
		if t == "" {
			return nil
		}
		s.dispatch(ctx, annotations.Event{
			Type:    t,
			When:    time.Now(),
			Project: p.Drift.Project,
			Stack:   p.Drift.Stack,
			Env:     p.Drift.Env,
			Outcome: p.Drift.Outcome,
			Message: p.Drift.Error,
			Tags: map[string]string{
				"fingerprint": p.Drift.Fingerprint,
				"run_id":      p.Drift.RunID,
			},
		})
	case p.PR != nil:
		t := prEventType(p.Event)
		if t == "" {
			return nil
		}
		s.dispatch(ctx, annotations.Event{
			Type:      t,
			When:      time.Now(),
			PR:        p.PR.PR,
			CommitSHA: p.PR.CommitSHA,
			Outcome:   string(p.Event),
		})
	}
	return nil
}

func driftEventType(e notify.Event) annotations.EventType {
	switch e {
	case notify.EventDriftDetected:
		return annotations.EventDriftDetected
	case notify.EventDriftResolved:
		return annotations.EventDriftResolved
	}
	return ""
}

func prEventType(e notify.Event) annotations.EventType {
	switch e {
	case notify.EventApplying:
		return annotations.EventApplyStarted
	case notify.EventApplied:
		return annotations.EventApplyCompleted
	case notify.EventFailed:
		return annotations.EventApplyFailed
	}
	return ""
}

// dispatch posts an annotation to every subscribed emitter. A delivery
// failure does not fail notification delivery - annotations are auxiliary -
// but it is logged rather than dropped. These errors used to be discarded
// entirely, so a misconfigured collector was indistinguishable from a
// healthy one.
func (s *Channel) dispatch(ctx context.Context, e annotations.Event) {
	for _, err := range annotations.Dispatch(ctx, s.emitters, e) {
		slog.Warn("annotation delivery failed",
			"channel", s.name, "event", string(e.Type), "error", err)
	}
}
