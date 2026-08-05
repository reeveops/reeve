package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseDurationExtended parses a Go duration string, additionally accepting
// day and week units that time.ParseDuration rejects: "7d" = 7*24h,
// "2w" = 14*24h (fractions like "1.5d" work too). Plain Go durations pass
// through unchanged. Config fields documented with day/week examples
// (drift state_bootstrap.baseline_max_age, suppression --until) validate
// against this; everything else uses plain time.ParseDuration because
// that is what the runtime consumers parse with.
func ParseDurationExtended(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var unit time.Duration
	switch {
	case strings.HasSuffix(s, "d"):
		unit = 24 * time.Hour
	case strings.HasSuffix(s, "w"):
		unit = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid duration %q (Go duration like 24h, or day/week units like 7d, 2w)", s)
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(s, "d"), "w"), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q (Go duration like 24h, or day/week units like 7d, 2w)", s)
	}
	return time.Duration(n * float64(unit)), nil
}

// validateDurations rejects any config-sourced duration field that does not
// parse. Historically the extractors silently fell back to a default on a
// parse error, so `ttl: 15minutes` quietly became the 4h default lock TTL
// and a typo'd freshness window silently disabled itself. Durations now
// fail closed at load/validate time (and therefore in `reeve lint`), with
// the file and field named.
func (c *Config) validateDurations() error {
	check := func(file, field, value string) error {
		if value == "" {
			return nil
		}
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("%s: %s: invalid duration %q (Go duration, e.g. \"90m\" or \"4h\", not \"15minutes\" or \"2d\")", file, field, value)
		}
		return nil
	}
	// checkPos additionally rejects zero and negative durations. time.ParseDuration
	// happily accepts "0s" and "-1h", but a non-positive lock TTL, timeout, or
	// freshness window is nonsensical and actively unsafe: a locking.ttl of 0s or
	// negative produces a born-expired lease whose heartbeat (ttl/3 <= 0) is a
	// no-op, so a concurrent run evicts the live holder and two applies race the
	// same stack. Fail closed here rather than silently defaulting downstream.
	checkPos := func(file, field, value string) error {
		if value == "" {
			return nil
		}
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s: %s: invalid duration %q (Go duration, e.g. \"90m\" or \"4h\", not \"15minutes\" or \"2d\")", file, field, value)
		}
		if d <= 0 {
			return fmt.Errorf("%s: %s: duration must be positive, got %q", file, field, value)
		}
		return nil
	}
	checkExtended := func(file, field, value string) error {
		if value == "" {
			return nil
		}
		if _, err := ParseDurationExtended(value); err != nil {
			return fmt.Errorf("%s: %s: %w", file, field, err)
		}
		return nil
	}
	// checkExtendedPos accepts day/week units AND rejects non-positive values,
	// so a bound like timeout_per_stack or a flap-damping window can't silently
	// become a no-op (0s) or a nonsensical negative. Omit the key for "no bound".
	checkExtendedPos := func(file, field, value string) error {
		if value == "" {
			return nil
		}
		d, err := ParseDurationExtended(value)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", file, field, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: %s: duration must be positive (omit the key for no bound), got %q", file, field, value)
		}
		return nil
	}

	if s := c.Shared; s != nil {
		if err := checkPos("shared.yaml", "locking.ttl", s.Locking.TTL); err != nil {
			return err
		}
		if err := checkPos("shared.yaml", "locking.reaper_interval", s.Locking.ReaperInterval); err != nil {
			return err
		}
		if err := check("shared.yaml", "retention.max_age", s.Retention.MaxAge); err != nil {
			return err
		}
		if err := checkPos("shared.yaml", "preconditions.preview_freshness", s.Preconditions.PreviewFreshness); err != nil {
			return err
		}
		if err := checkPos("shared.yaml", "approvals.default.freshness", s.Approvals.Default.Freshness); err != nil {
			return err
		}
		for _, pattern := range sortedKeys(s.Approvals.Stacks) {
			if err := checkPos("shared.yaml", fmt.Sprintf("approvals.stacks[%s].freshness", pattern), s.Approvals.Stacks[pattern].Freshness); err != nil {
				return err
			}
		}
		for _, w := range s.FreezeWindows {
			if err := checkPos("shared.yaml", fmt.Sprintf("freeze_windows[%s].duration", w.Name), w.Duration); err != nil {
				return err
			}
		}
	}

	for _, e := range c.Engines {
		file := fmt.Sprintf("engine config (engine.type=%s)", e.Engine.Type)
		if err := checkPos(file, "engine.execution.preview_timeout", e.Engine.Execution.PreviewTimeout); err != nil {
			return err
		}
		if err := checkPos(file, "engine.execution.apply_timeout", e.Engine.Execution.ApplyTimeout); err != nil {
			return err
		}
	}

	if d := c.Drift; d != nil {
		if err := checkPos("drift.yaml", "freshness.window", d.Freshness.Window); err != nil {
			return err
		}
		if err := checkExtended("drift.yaml", "behavior.state_bootstrap.baseline_max_age", d.Behavior.StateBootstrap.BaselineMaxAge); err != nil {
			return err
		}
		if err := checkExtendedPos("drift.yaml", "behavior.renotify_after", d.Behavior.RenotifyAfter); err != nil {
			return err
		}
		if err := checkExtendedPos("drift.yaml", "behavior.timeout_per_stack", d.Behavior.TimeoutPerStack); err != nil {
			return err
		}
	}

	if a := c.Auth; a != nil {
		for _, name := range sortedKeys(a.Providers) {
			p := a.Providers[name]
			if err := check("auth.yaml", fmt.Sprintf("providers[%s].duration", name), p.Duration); err != nil {
				return err
			}
			if err := check("auth.yaml", fmt.Sprintf("providers[%s].ttl", name), p.TTL); err != nil {
				return err
			}
		}
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
