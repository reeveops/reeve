package approvals

import (
	"strconv"
	"strings"
)

// OverlapScanError reports a PARTIAL PR-overlap scan: some open PRs could
// not be checked (changed-file fetch failed, or the scan was capped). The
// result slice returned alongside this error still carries every PR that
// WAS checked, so callers must not treat the error as "no overlap" -
// degrade to a warning naming the unchecked PRs instead.
type OverlapScanError struct {
	// Unchecked lists the PR numbers whose changed files could not be
	// inspected.
	Unchecked []int
	// MoreBeyondCap is set when the scan stopped at its PR cap while more open
	// PRs existed. Those PRs' numbers are unknown (never fetched), so they are
	// reported as a count-less flag rather than silently dropped.
	MoreBeyondCap bool
	// Err is the first underlying fetch error, kept for diagnostics.
	Err error
}

func (e *OverlapScanError) Error() string {
	var parts []string
	if len(e.Unchecked) > 0 {
		nums := make([]string, len(e.Unchecked))
		for i, n := range e.Unchecked {
			nums[i] = "#" + strconv.Itoa(n)
		}
		parts = append(parts, "could not check open PR(s) "+strings.Join(nums, ", "))
	}
	if e.MoreBeyondCap {
		parts = append(parts, "additional open PRs beyond the scan cap were not checked")
	}
	msg := "overlap scan incomplete: " + strings.Join(parts, "; ")
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *OverlapScanError) Unwrap() error { return e.Err }
