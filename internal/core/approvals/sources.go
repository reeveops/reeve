package approvals

import "strings"

// Source type identifiers. These are the values accepted in
// `approvals.sources[].type` and stamped onto Approval.Source.
const (
	SourcePRReview  = "pr_review"
	SourcePRComment = "pr_comment"
)

// DefaultCommentCommand is the trigger phrase for the pr_comment source when
// `approvals.sources[].command` is left unset. It mirrors the `/reeve approve`
// convention documented in the approvals spec.
const DefaultCommentCommand = "/reeve approve"

// PRReviewEnabled reports whether the pr_review source is active. It is on by
// default and only turns off when an explicit `sources` entry names pr_review
// with `enabled: false`. This keeps existing configs (no `sources` block)
// behaving exactly as before: pr_review only.
func (c Config) PRReviewEnabled() bool {
	for _, s := range c.Sources {
		if s.Type == SourcePRReview {
			return s.Enabled
		}
	}
	return true
}

// PRCommentEnabled reports whether the opt-in pr_comment source is active. It
// is off unless an explicit `sources` entry names pr_comment with
// `enabled: true`.
func (c Config) PRCommentEnabled() bool {
	for _, s := range c.Sources {
		if s.Type == SourcePRComment {
			return s.Enabled
		}
	}
	return false
}

// CommentCommand returns the configured `/reeve approve` trigger phrase for the
// pr_comment source, or DefaultCommentCommand when unset.
func (c Config) CommentCommand() string {
	for _, s := range c.Sources {
		if s.Type == SourcePRComment && strings.TrimSpace(s.Command) != "" {
			return strings.TrimSpace(s.Command)
		}
	}
	return DefaultCommentCommand
}

// MergeApprovals concatenates approvals gathered from multiple sources into a
// single slice. Deduplication by approver identity is intentionally deferred to
// Evaluate, which counts each distinct login at most once - so a human who
// approves via both pr_review AND pr_comment counts a single time toward the
// gate. Merge keeps every raw event so that per-approval rules
// (dismiss_on_new_commit, freshness, the non-author rule) still apply uniformly
// across sources before that identity dedup happens.
func MergeApprovals(sets ...[]Approval) []Approval {
	n := 0
	for _, s := range sets {
		n += len(s)
	}
	out := make([]Approval, 0, n)
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}
