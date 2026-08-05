package drift

import (
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// PermanentSuppression is a config-level (drift.yaml `permanent_suppressions`)
// silence for drift you have accepted as reality. It is the always-on twin of
// the imperative suppress store: a matched stack is still checked and its
// state persisted (so resolution is still tracked), but its drift-lifecycle
// events are not dispatched. Unlike the store's time-bounded suppressions, a
// permanent suppression does not skip the check.
type PermanentSuppression struct {
	// Stack is a glob over "project/stack" (doublestar).
	Stack string
	// Reason is surfaced in the report.
	Reason string
	// Until optionally bounds the suppression. Zero = permanent.
	Until time.Time
}

// active reports whether the suppression is in effect at now.
func (p PermanentSuppression) active(now time.Time) bool {
	return p.Until.IsZero() || now.Before(p.Until)
}

// matchPermanentSuppression returns the first active permanent suppression
// whose glob matches ref ("project/stack"), and whether one matched.
func matchPermanentSuppression(sups []PermanentSuppression, ref string, now time.Time) (PermanentSuppression, bool) {
	for _, s := range sups {
		if !s.active(now) {
			continue
		}
		// Stack refs are path-like ("project/stack"); use doublestar so `*`
		// respects the `/` boundary, consistent with scope include/exclude.
		if ok, err := doublestar.Match(s.Stack, ref); err == nil && ok {
			return s, true
		}
	}
	return PermanentSuppression{}, false
}
