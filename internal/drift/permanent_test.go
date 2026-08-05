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

func TestMatchPermanentSuppression(t *testing.T) {
	now := time.Now()
	sups := []PermanentSuppression{
		{Stack: "prod/legacy-*", Reason: "vendor-managed"},
		{Stack: "dev/scratch", Until: now.Add(-time.Hour)}, // expired
	}
	if s, ok := matchPermanentSuppression(sups, "prod/legacy-erp", now); !ok || s.Reason != "vendor-managed" {
		t.Fatalf("glob should match prod/legacy-erp, got ok=%v s=%+v", ok, s)
	}
	if _, ok := matchPermanentSuppression(sups, "prod/api", now); ok {
		t.Fatal("prod/api must not match")
	}
	if _, ok := matchPermanentSuppression(sups, "dev/scratch", now); ok {
		t.Fatal("expired suppression must not match")
	}
}

func runOneSuppressed(t *testing.T, res iac.PreviewResult, checkErr error) (Item, Event, *StateStore) {
	t.Helper()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatalf("filesystem store: %v", err)
	}
	store := &StateStore{Blob: fs}
	opts := Options{
		Engine:     fakeEngine{res: res, err: checkErr},
		Redactor:   redact.New(),
		StateStore: store,
		PermanentSuppressions: []PermanentSuppression{
			{Stack: "prod/*", Reason: "accepted drift"},
		},
	}
	item, ev, skip, _ := runOne(context.Background(), opts, discovery.Stack{Project: "prod", Name: "api", Path: "prod/api"}, time.Now())
	if skip {
		t.Fatal("permanent suppression must NOT skip the check")
	}
	return item, ev, store
}

func TestPermanentSuppressionSilencesDriftButKeepsState(t *testing.T) {
	res := iac.PreviewResult{Counts: summary.Counts{Change: 1}, DriftedURNs: []string{"urn::x"}}
	item, ev, store := runOneSuppressed(t, res, nil)

	if item.Outcome != OutcomeDriftDetected {
		t.Fatalf("suppressed stack still records the true outcome, got %s", item.Outcome)
	}
	if ev != EventNone {
		t.Fatalf("permanent suppression must silence drift dispatch, got %s", ev)
	}
	// Composition guard (#47 damping x #50 suppression): #47 introduced a
	// SEPARATE NotifyEvent (the actual channel-dispatch signal) that runs
	// through flap damping. With RenotifyAfter unset, damping passes a fresh
	// detection straight through, so NotifyEvent would be EventDriftDetected
	// unless permanent suppression ALSO silences it. Both signals must be quiet
	// or a suppressed stack still notifies.
	if item.NotifyEvent != EventNone {
		t.Fatalf("permanent suppression must ALSO silence NotifyEvent (damping path), got %s", item.NotifyEvent)
	}
	if item.Event != EventNone {
		t.Fatalf("permanent suppression must silence item.Event, got %s", item.Event)
	}
	if !item.Suppressed || item.SuppressReason != "accepted drift" {
		t.Fatalf("item must be flagged suppressed with its reason, got %+v", item)
	}
	// State persisted so resolution is tracked.
	st, err := store.Load(context.Background(), "prod", "api")
	if err != nil || st.LastOutcome != OutcomeDriftDetected {
		t.Fatalf("state must be persisted for a suppressed stack, got %+v err=%v", st, err)
	}
	if st.Fingerprint == "" {
		t.Fatal("suppressed drift must still persist its fingerprint")
	}
}

// TestPermanentSuppressionSilencesBothEventAndNotifyEvent proves the #46/#47
// (durability + flap damping) x #50 (permanent suppression) composition end to
// end: a permanently-suppressed, actively-drifted stack must silence BOTH the
// classification event (ev / item.Event, drives reports/exit_on/OTEL) and the
// damping-produced item.NotifyEvent (drives channel dispatch), while still
// persisting state so a later resolution is tracked. RenotifyAfter is set and
// combined across two runs so the damping path is genuinely live - it would
// deliver a NotifyEvent if suppression did not silence it.
func TestPermanentSuppressionSilencesBothEventAndNotifyEvent(t *testing.T) {
	ctx := context.Background()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatalf("filesystem store: %v", err)
	}
	store := &StateStore{Blob: fs}
	stack := discovery.Stack{Project: "prod", Name: "api", Path: "prod/api"}
	drifted := iac.PreviewResult{Counts: summary.Counts{Change: 1}, DriftedURNs: []string{"urn::x"}}
	opts := Options{
		Engine:                fakeEngine{res: drifted},
		Redactor:              redact.New(),
		StateStore:            store,
		RenotifyAfter:         24 * time.Hour, // damping active
		PermanentSuppressions: []PermanentSuppression{{Stack: "prod/*", Reason: "accepted"}},
	}
	t0 := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	// Run 1: fresh drift under permanent suppression. A fresh detection is
	// exactly what damping would deliver (NotifyEvent=drift_detected) - so this
	// is where a missing NotifyEvent silence would leak an alert.
	item, ev, skip, _ := runOne(ctx, opts, stack, t0)
	if skip {
		t.Fatal("permanent suppression must not skip the check")
	}
	if ev != EventNone || item.Event != EventNone || item.NotifyEvent != EventNone {
		t.Fatalf("run1 both signals must be silent: ev=%s item.Event=%s NotifyEvent=%s", ev, item.Event, item.NotifyEvent)
	}
	if item.Outcome != OutcomeDriftDetected || !item.Suppressed {
		t.Fatalf("run1 true outcome/suppressed flag lost: outcome=%s suppressed=%v", item.Outcome, item.Suppressed)
	}
	st, err := store.Load(ctx, "prod", "api")
	if err != nil || st.LastOutcome != OutcomeDriftDetected {
		t.Fatalf("run1 state must persist the drift for resolution tracking: %+v err=%v", st, err)
	}

	// Run 2: still drifted a day later - the ongoing/re-alert path is likewise
	// silenced. State keeps tracking the episode.
	item, ev, _, _ = runOne(ctx, opts, stack, t0.Add(25*time.Hour))
	if ev != EventNone || item.NotifyEvent != EventNone {
		t.Fatalf("run2 ongoing under suppression must stay silent: ev=%s NotifyEvent=%s", ev, item.NotifyEvent)
	}
	if item.Outcome != OutcomeDriftDetected {
		t.Fatalf("run2 outcome still drift_detected, got %s", item.Outcome)
	}
}

func TestPermanentSuppressionDoesNotHideErrors(t *testing.T) {
	item, ev, _ := runOneSuppressed(t, iac.PreviewResult{Error: "engine crashed"}, nil)
	if item.Outcome != OutcomeError {
		t.Fatalf("want error outcome, got %s", item.Outcome)
	}
	if ev != EventCheckFailed {
		t.Fatalf("permanent suppression must NOT hide check_failed, got %s", ev)
	}
}
