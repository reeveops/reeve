package freeze

import (
	"testing"
	"time"
)

func TestActiveForFridayAfternoon(t *testing.T) {
	cfg := Config{Windows: []Window{{
		Name: "friday-afternoon", Cron: "0 15 * * 5",
		Duration: 65 * time.Hour, // through monday morning
		Stacks:   []string{"prod/*"},
	}}}

	// Friday 2026-04-24 16:00 UTC: window fires at 15:00, still active.
	fri := time.Date(2026, 4, 24, 16, 0, 0, 0, time.UTC)
	name, active, err := ActiveFor(cfg, "prod/api", fri)
	if err != nil {
		t.Fatal(err)
	}
	if !active || name != "friday-afternoon" {
		t.Fatalf("expected friday-afternoon active, got %q active=%v", name, active)
	}

	// Tuesday afternoon: window has long expired.
	tue := time.Date(2026, 4, 28, 16, 0, 0, 0, time.UTC)
	_, active, err = ActiveFor(cfg, "prod/api", tue)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expected tuesday to be outside freeze")
	}
}

func TestFreezeDoesNotApplyToNonMatchingStack(t *testing.T) {
	cfg := Config{Windows: []Window{{
		Name: "prod-only", Cron: "0 0 * * *", Duration: time.Hour, Stacks: []string{"prod/*"},
	}}}
	// Any time, dev stack should never be in freeze.
	now := time.Date(2026, 4, 24, 0, 30, 0, 0, time.UTC)
	_, active, err := ActiveFor(cfg, "dev/api", now)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("dev stack should not be in prod-only freeze")
	}
}

func TestLongWindowStaysFrozenPastTwoWeeks(t *testing.T) {
	// Regression: the fire-scan horizon was hardcoded to 14 days, so a
	// window longer than that lifted itself in the back half of its own
	// range - applies that should have been blocked went through.
	cfg := Config{Windows: []Window{{
		Name: "holiday", Cron: "0 0 15 12 *", // midnight, Dec 15, yearly
		Duration: 21 * 24 * time.Hour, // through Jan 5
	}}}

	// Dec 30: 15 days into a 21-day freeze. Still frozen.
	deep := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	name, active, err := ActiveFor(cfg, "prod/api", deep)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("day 15 of a 21-day freeze reported not-frozen (fail-open)")
	}
	if name != "holiday" {
		t.Fatalf("expected window %q, got %q", "holiday", name)
	}
}

func TestLongWindowExpiresOnSchedule(t *testing.T) {
	// The other side of the horizon fix: past the duration, it must lift.
	cfg := Config{Windows: []Window{{
		Name: "holiday", Cron: "0 0 15 12 *",
		Duration: 21 * 24 * time.Hour, // Dec 15 -> Jan 5
	}}}
	after := time.Date(2027, 1, 6, 12, 0, 0, 0, time.UTC)
	_, active, err := ActiveFor(cfg, "prod/api", after)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("freeze still active a day after its window closed")
	}
}

func TestDenseCronExhaustingScanFailsClosed(t *testing.T) {
	// A per-minute cron over a 30-day window has far more fires than
	// maxFireScan. Truncating the walk must leave the gate closed, since
	// every unvisited fire is nearer to now than the one we stopped on.
	cfg := Config{Windows: []Window{{
		Name: "always", Cron: "* * * * *",
		Duration: 30 * 24 * time.Hour,
	}}}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	name, active, err := ActiveFor(cfg, "prod/api", now)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("scan-cap exhaustion reported not-frozen (fail-open)")
	}
	if name != "always" {
		t.Fatalf("expected window %q, got %q", "always", name)
	}
}

func TestInvalidCronBubblesError(t *testing.T) {
	cfg := Config{Windows: []Window{{Name: "bad", Cron: "nonsense"}}}
	_, _, err := ActiveFor(cfg, "prod/api", time.Now())
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestActiveForBadCronFailsClosed(t *testing.T) {
	// A window covering the stack with an unparseable cron must return an
	// error (the caller fails closed on it) rather than silently reporting
	// "not in freeze".
	cfg := Config{Windows: []Window{
		{Name: "bad", Cron: "not a cron", Duration: time.Hour}, // empty Stacks = all
	}}
	_, active, err := ActiveFor(cfg, "prod/api", time.Now())
	if err == nil {
		t.Fatal("expected error for bad cron, got nil (would fail open)")
	}
	if active {
		t.Fatal("active must be false when evaluation errored")
	}
}
