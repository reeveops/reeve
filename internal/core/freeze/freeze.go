// Package freeze evaluates freeze windows. A window is a cron expression
// + duration + stack pattern set. A stack is in freeze if *any* window
// matching it has fired within the last `duration` at now.
package freeze

import (
	"fmt"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/robfig/cron/v3"
)

// Window is a single freeze entry.
type Window struct {
	Name     string
	Cron     string        // e.g. "0 15 * * 5"
	Duration time.Duration // how long the freeze lasts after firing
	Stacks   []string      // glob patterns over "project/stack"; empty = all stacks
}

// Config is the full set from shared.yaml.
type Config struct {
	Windows []Window
}

// ActiveFor returns the first window currently freezing the given stack
// ref, or "" if none is active.
func ActiveFor(cfg Config, ref string, now time.Time) (string, bool, error) {
	for _, w := range cfg.Windows {
		match := len(w.Stacks) == 0
		for _, pat := range w.Stacks {
			if ok, _ := doublestar.Match(pat, ref); ok {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		firing, err := mostRecentFire(w.Cron, w.Duration, now)
		if err != nil {
			return "", false, fmt.Errorf("freeze %q: %w", w.Name, err)
		}
		if firing.IsZero() {
			continue
		}
		if now.Before(firing.Add(w.Duration)) {
			return w.Name, true, nil
		}
	}
	return "", false, nil
}

// maxFireScan bounds the forward walk in mostRecentFire so a pathological
// cron (say, every minute across a year-long window) cannot spin. Hitting it
// is safe by construction - see the comment at the end of mostRecentFire.
const maxFireScan = 10_000

// mostRecentFire returns the most recent scheduled fire at or before now, or
// the zero time if the window's cron has not fired within `window` of now.
//
// The scan-back horizon is the window's own duration, not a fixed span: a
// fire older than now-window cannot freeze anything, because ActiveFor's
// test is `now < fire+window`. The previous implementation hardcoded a
// 14-day horizon, so any window longer than 14 days silently stopped firing
// in the back half of its own range - a 21-day holiday freeze lifted itself
// around day 15 and let applies through. That was a fail-open in a gate.
func mostRecentFire(expr string, window time.Duration, now time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, err
	}
	if window <= 0 {
		// A non-positive duration cannot freeze anything. Config validation
		// rejects these (durations.go checkPos), so this is belt-and-braces.
		return time.Time{}, nil
	}
	// Robfig cron's Next takes "from t, return next occurrence after t", so
	// walk forward from the oldest fire that could still be freezing.
	cur := now.Add(-window)
	last := time.Time{}
	for i := 0; i < maxFireScan; i++ {
		next := sched.Next(cur)
		if next.After(now) {
			return last, nil
		}
		last = next
		cur = next
	}
	// Scan cap exhausted: there are further fires between `last` and now,
	// but every one of them is inside [now-window, now], so `last` already
	// proves the freeze is active and ActiveFor reports frozen. Truncating
	// here keeps the gate closed rather than open.
	return last, nil
}
