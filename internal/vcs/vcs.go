// Package vcs defines the version-control system abstraction. Adapters
// (internal/vcs/github, future: gitlab) satisfy small use-site interfaces
// declared by their consumers. This file holds shared types referenced
// across adapters and core.
package vcs

// PR is the minimum normalized PR shape consumed across reeve.
type PR struct {
	Number  int
	HeadSHA string
	BaseRef string
	Title   string
	Author  string
	IsFork  bool
	IsDraft bool
	// RepoPrivate reports whether the base repository is private. It gates
	// the approvals safety default: on a public repo any GitHub user can
	// submit an approving review, so unlisted reviews are not counted
	// unless explicitly opted in. Defaults false (public) when an adapter
	// cannot determine visibility, which is the fail-closed (stricter)
	// choice.
	RepoPrivate bool
	OpenedAt    string // RFC3339
	URL         string
}

// CommentCapabilities describes optional VCS features. Capability detection
// avoids hard-coding per-platform behavior in core.
type CommentCapabilities struct {
	SupportsEdit bool // GitHub + GitLab = true; false triggers append fallback
}

// CommentApprovalConfig configures the opt-in pr_comment approval source. The
// adapter reads historical PR comments directly (not the dispatched event), so
// it MUST re-enforce the same author_association authorization gate that
// action.yml applies to `/reeve ...` commands - a comment from an unauthorized
// commenter is not vouched for by anything upstream. All fields are populated
// from the same action inputs that gate command dispatch.
type CommentApprovalConfig struct {
	// Command is the full trigger phrase, e.g. "/reeve approve". Its first
	// token is accepted as a command prefix in addition to CommandPrefixes;
	// its remaining tokens form the verb that must follow the prefix.
	Command string
	// CommandPrefixes is the set of accepted comment prefixes (default
	// "/reeve"), mirroring the action's command-prefix input.
	CommandPrefixes []string
	// AllowedAssociations is the author_association allowlist (e.g. OWNER,
	// MEMBER, COLLABORATOR). Values are compared case-insensitively. A comment
	// whose author_association is not listed never counts as an approval.
	AllowedAssociations []string
}

// ChecksGreenOpts controls which check_runs ChecksGreen treats as "self" and
// therefore skips when computing the green/red verdict. The defaults (zero
// value) skip nothing - callers must populate this from their CI environment.
//
// Two skip mechanisms exist because each handles a different failure mode:
//
//   - IgnoreRunID skips check_runs created by the *current* CI run (e.g.
//     GitHub Actions GITHUB_RUN_ID). Required because the run cannot be
//     green while it is itself running.
//   - IgnoreNames skips check_runs whose name appears in this list,
//     regardless of which run produced them. Required because a previous
//     failed reeve apply leaves a `conclusion=failure` check_run on the same
//     SHA; without a name-based skip every subsequent apply on that SHA
//     would fail the gate forever. Match is exact and case-sensitive.
type ChecksGreenOpts struct {
	IgnoreRunID int64
	IgnoreNames []string
}
