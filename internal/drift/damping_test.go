package drift

import (
	"context"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

func TestDampNotificationDefaultsPreserveBehavior(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	// renotify unset: everything passes through.
	for _, ev := range []Event{EventDriftDetected, EventDriftOngoing, EventCheckFailed, EventCheckRecovered, EventNone} {
		got, _ := dampNotification(State{LastNotifiedAt: now.Add(-time.Minute), OngoingSince: now.Add(-time.Hour)}, ev, now, 0)
		if got != ev {
			t.Fatalf("renotify=0: %s must pass through, got %s", ev, got)
		}
	}
	// Detected stamps the notification time even without damping, so a
	// later renotify_after rollout has history to work from.
	if _, notified := dampNotification(State{}, EventDriftDetected, now, 0); !notified {
		t.Fatal("delivered detection must stamp LastNotifiedAt")
	}
	// Resolved for an episode notified under legacy state (no LastNotifiedAt)
	// must still deliver.
	if got, _ := dampNotification(State{OngoingSince: now.Add(-time.Hour)}, EventDriftResolved, now, 0); got != EventDriftResolved {
		t.Fatalf("legacy-state resolve must deliver, got %s", got)
	}
}

func TestDampNotificationFlapWindow(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	renotify := 24 * time.Hour

	// Fresh detection, never notified: delivers.
	got, notified := dampNotification(State{}, EventDriftDetected, now, renotify)
	if got != EventDriftDetected || !notified {
		t.Fatalf("fresh detection: got %s notified=%v", got, notified)
	}

	// Re-detection 1h after the last alert (a flap): silenced.
	prev := State{LastNotifiedAt: now.Add(-time.Hour), OngoingSince: now}
	got, notified = dampNotification(prev, EventDriftDetected, now, renotify)
	if got != EventNone || notified {
		t.Fatalf("flap within window must be silent, got %s notified=%v", got, notified)
	}

	// Re-detection after the window: delivers again.
	prev = State{LastNotifiedAt: now.Add(-25 * time.Hour)}
	got, notified = dampNotification(prev, EventDriftDetected, now, renotify)
	if got != EventDriftDetected || !notified {
		t.Fatalf("post-window detection must deliver, got %s notified=%v", got, notified)
	}

	// Ongoing inside the window: silent.
	prev = State{LastNotifiedAt: now.Add(-time.Hour), OngoingSince: now.Add(-2 * time.Hour)}
	if got, _ = dampNotification(prev, EventDriftOngoing, now, renotify); got != EventNone {
		t.Fatalf("ongoing within window must be silent, got %s", got)
	}

	// Ongoing past the window: re-alerts AS drift_detected and restamps.
	prev = State{LastNotifiedAt: now.Add(-25 * time.Hour), OngoingSince: now.Add(-48 * time.Hour)}
	got, notified = dampNotification(prev, EventDriftOngoing, now, renotify)
	if got != EventDriftDetected || !notified {
		t.Fatalf("ongoing past window must re-alert as detected, got %s notified=%v", got, notified)
	}
}

func TestDampNotificationRecoveryOncePerNotifiedEpisode(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	renotify := 24 * time.Hour

	// Episode was notified (alert at/after episode start): resolve delivers.
	prev := State{LastNotifiedAt: now.Add(-time.Hour), OngoingSince: now.Add(-time.Hour)}
	if got, _ := dampNotification(prev, EventDriftResolved, now, renotify); got != EventDriftResolved {
		t.Fatalf("notified episode must get a recovery notice, got %s", got)
	}

	// Episode was a silenced flap (last alert predates this episode):
	// suppress the resolve too - channels never saw the detection.
	prev = State{LastNotifiedAt: now.Add(-3 * time.Hour), OngoingSince: now.Add(-time.Hour)}
	if got, _ := dampNotification(prev, EventDriftResolved, now, renotify); got != EventNone {
		t.Fatalf("silenced episode must not emit a recovery notice, got %s", got)
	}
}

// TestRunOneFlapDampingEndToEnd drives a drifted→clean→drifted oscillation
// through runOne with persisted state and verifies only the first detection
// notifies within the window, while classification events stay intact for
// exit_on/reporting.
func TestRunOneFlapDampingEndToEnd(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ss := &StateStore{Blob: fs}
	stack := discovery.Stack{Project: "p", Name: "s", Path: "p/s"}
	t0 := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	drifted := fakeEngine{res: iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn:x"},
	}}
	clean := fakeEngine{}
	opts := func(e Engine) Options {
		return Options{Engine: e, Redactor: redact.New(), StateStore: ss, RenotifyAfter: 24 * time.Hour}
	}

	// Run 1: drift appears - notifies.
	item, ev, _, _ := runOne(ctx, opts(drifted), stack, t0)
	if ev != EventDriftDetected || item.NotifyEvent != EventDriftDetected {
		t.Fatalf("run1: ev=%s notify=%s", ev, item.NotifyEvent)
	}

	// Run 2: clean - recovery notice (episode was notified).
	item, ev, _, _ = runOne(ctx, opts(clean), stack, t0.Add(time.Hour))
	if ev != EventDriftResolved || item.NotifyEvent != EventDriftResolved {
		t.Fatalf("run2: ev=%s notify=%s", ev, item.NotifyEvent)
	}

	// Run 3: drifted again 2h later - classified as detected (exit_on and
	// the report still see it) but the notification is damped.
	item, ev, _, _ = runOne(ctx, opts(drifted), stack, t0.Add(2*time.Hour))
	if ev != EventDriftDetected {
		t.Fatalf("run3 classification must stay drift_detected, got %s", ev)
	}
	if item.NotifyEvent != EventNone {
		t.Fatalf("run3 notification must be damped, got %s", item.NotifyEvent)
	}

	// Run 4: clean again - no repeat recovery notice for the silenced flap.
	item, ev, _, _ = runOne(ctx, opts(clean), stack, t0.Add(3*time.Hour))
	if ev != EventDriftResolved {
		t.Fatalf("run4 classification: %s", ev)
	}
	if item.NotifyEvent != EventNone {
		t.Fatalf("run4 recovery notice must be suppressed for a silenced flap, got %s", item.NotifyEvent)
	}

	// Run 5: drifted again after the window - notifies again.
	item, _, _, _ = runOne(ctx, opts(drifted), stack, t0.Add(30*time.Hour))
	if item.NotifyEvent != EventDriftDetected {
		t.Fatalf("run5 post-window detection must notify, got %s", item.NotifyEvent)
	}
}
