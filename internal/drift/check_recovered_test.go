package drift

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/notify"
)

func TestRunOneCheckRecoveredAfterError(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ss := &StateStore{Blob: fs}
	stack := discovery.Stack{Project: "p", Name: "s", Path: "p/s"}
	now := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	// Run 1: check fails.
	failOpts := Options{Engine: fakeEngine{err: errors.New("auth exploded")}, Redactor: redact.New(), StateStore: ss}
	item, ev, _, _ := runOne(ctx, failOpts, stack, now)
	if ev != EventCheckFailed || item.CheckRecovered {
		t.Fatalf("run1: ev=%v recovered=%v", ev, item.CheckRecovered)
	}

	// Run 2: check succeeds (no drift). Classification is silent, but the
	// recovery must be flagged so channels can resolve the failure alert.
	okOpts := Options{Engine: fakeEngine{}, Redactor: redact.New(), StateStore: ss}
	item, ev, _, _ = runOne(ctx, okOpts, stack, now.Add(time.Hour))
	if ev != EventNone {
		t.Fatalf("run2 classification: %v", ev)
	}
	if !item.CheckRecovered {
		t.Fatal("first success after a failed check must set CheckRecovered")
	}

	// Run 3: still healthy - no repeat recovery.
	item, _, _, _ = runOne(ctx, okOpts, stack, now.Add(2*time.Hour))
	if item.CheckRecovered {
		t.Fatal("recovery must fire once, not on every healthy run")
	}
}

func TestNotifyPayloadsEmitsCheckRecovered(t *testing.T) {
	out := &RunOutput{
		RunID: "drift-2",
		Items: []Item{
			// Recovered AND newly drifted: both payloads, recovery first.
			{Project: "api", Stack: "prod", Outcome: OutcomeDriftDetected, CheckRecovered: true, NotifyEvent: EventDriftDetected},
			// Recovered with a silent classification (error -> no_drift when
			// the pre-error baseline was already no_drift): recovery only.
			{Project: "web", Stack: "prod", Outcome: OutcomeNoDrift, CheckRecovered: true},
			// Plain healthy stack: nothing.
			{Project: "db", Stack: "prod", Outcome: OutcomeNoDrift},
		},
		Events: []Event{EventDriftDetected, EventNone, EventNone},
	}
	got := NotifyPayloads(out)
	if len(got) != 3 {
		t.Fatalf("want 3 payloads, got %+v", got)
	}
	if got[0].Event != notify.EventCheckRecovered || got[0].Drift.Ref() != "api/prod" {
		t.Fatalf("recovery must precede the fresh alert: %+v", got[0])
	}
	if got[1].Event != notify.EventDriftDetected || got[1].Drift.Ref() != "api/prod" {
		t.Fatalf("payload 1: %+v", got[1])
	}
	if got[2].Event != notify.EventCheckRecovered || got[2].Drift.Ref() != "web/prod" {
		t.Fatalf("silent classification must still emit recovery: %+v", got[2])
	}
}
