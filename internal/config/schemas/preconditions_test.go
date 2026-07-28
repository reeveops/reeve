package schemas

import (
	"testing"
	"time"
)

// preview_freshness is on by default. Before this default existed, an
// operator who never wrote the key got no freshness gate at all - a plan of
// any age could be applied, silently.
func TestPreviewFreshnessDefaultsToFourHours(t *testing.T) {
	d, enabled := PreconditionsYAML{}.ResolvedPreviewFreshness()
	if !enabled {
		t.Fatal("an omitted preview_freshness must enable the gate, not disable it")
	}
	if d != 4*time.Hour {
		t.Errorf("default = %s, want 4h", d)
	}
	if DefaultPreviewFreshness != 4*time.Hour {
		t.Errorf("DefaultPreviewFreshness = %s, want 4h", DefaultPreviewFreshness)
	}
}

func TestPreviewFreshnessExplicitValues(t *testing.T) {
	for _, tc := range []struct {
		in          string
		wantDur     time.Duration
		wantEnabled bool
	}{
		{"2h", 2 * time.Hour, true},
		{"45m", 45 * time.Minute, true},
		{" 90m ", 90 * time.Minute, true}, // whitespace is a typo, not an opt-out
		{"0", 0, false},                   // the deliberate opt-out
		{"0s", 0, false},
		{"-1h", 0, false}, // nonsense cannot mean "very fresh"
	} {
		d, enabled := PreconditionsYAML{PreviewFreshness: tc.in}.ResolvedPreviewFreshness()
		if enabled != tc.wantEnabled || d != tc.wantDur {
			t.Errorf("%q -> (%s, %v), want (%s, %v)", tc.in, d, enabled, tc.wantDur, tc.wantEnabled)
		}
	}
}

// plan_locking is on unless explicitly turned off, so the safe behavior is
// what an operator gets without reading the docs.
func TestPlanLockingDefaultsOn(t *testing.T) {
	if !(EngineBody{}).PlanLockingEnabled() {
		t.Error("omitted plan_locking must mean locked")
	}
	off := false
	if (EngineBody{PlanLocking: &off}).PlanLockingEnabled() {
		t.Error("plan_locking: false must turn locking off")
	}
	on := true
	if !(EngineBody{PlanLocking: &on}).PlanLockingEnabled() {
		t.Error("plan_locking: true must keep locking on")
	}
}
