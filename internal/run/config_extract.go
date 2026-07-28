package run

import (
	"fmt"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/core/breakglass"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/freeze"
	"github.com/FynxLabs/reeve/internal/core/preconditions"
	"github.com/FynxLabs/reeve/internal/core/render"
)

// PreviewTimeoutSec returns engine.execution.preview_timeout as whole seconds
// for PreviewOpts.TimeoutSec, or 0 (adapter default) when unset. Validated
// positive at config load, so a bad value never reaches here.
func PreviewTimeoutSec(e *schemas.Engine) int { return execTimeoutSec(e, true) }

// ApplyTimeoutSec returns engine.execution.apply_timeout as whole seconds for
// ApplyOpts.TimeoutSec, or 0 (adapter default) when unset.
func ApplyTimeoutSec(e *schemas.Engine) int { return execTimeoutSec(e, false) }

func execTimeoutSec(e *schemas.Engine, preview bool) int {
	if e == nil {
		return 0
	}
	v := e.Engine.Execution.ApplyTimeout
	if preview {
		v = e.Engine.Execution.PreviewTimeout
	}
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0
	}
	return int(d / time.Second)
}

// mappingNoticeFor returns a PR-comment banner explaining a non-normal
// change-mapping outcome, or "" for the normal matched case.
func mappingNoticeFor(res discovery.AffectedResult) string {
	switch res.Reason {
	case discovery.ReasonDocsOnly:
		return "Documentation/asset-only changes detected — no Pulumi stacks affected."
	case discovery.ReasonBroadened:
		shown := res.Unmapped
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("…and %d more", len(res.Unmapped)-5))
		}
		return fmt.Sprintf("Previewing all stacks: changed files map to no specific stack (e.g. shared/provider code): %s. Set `change_mapping.scope: pulumi_only` to disable.",
			strings.Join(shown, ", "))
	}
	return ""
}

// joinNotices concatenates two non-empty notice strings with a separator.
func joinNotices(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n>\n> " + b
}

// toPreconditionsConfig extracts the preconditions gate config from shared.yaml.
func toPreconditionsConfig(s *schemas.Shared) preconditions.Config {
	if s == nil {
		return preconditions.Config{}
	}
	out := preconditions.Config{
		FailOnForkPRs:           !s.Apply.AllowForkPRs,
		PreviewMaxCommitsBehind: s.Preconditions.PreviewMaxCommitsBehind,
	}
	if s.Preconditions.RequireUpToDate != nil {
		out.RequireUpToDate = *s.Preconditions.RequireUpToDate
	}
	if s.Preconditions.RequireChecksPassing != nil {
		out.RequireChecksPassing = *s.Preconditions.RequireChecksPassing
	}
	// An unparseable value cannot reach here - Config.Validate rejects it, so
	// a typo can no longer silently leave the gate at zero (= disabled).
	// "0" and "" mean deliberately disabled; see docs/configuration.md for
	// what that gives up.
	if d, err := time.ParseDuration(s.Preconditions.PreviewFreshness); err == nil {
		out.PreviewFreshness = d
	}
	return out
}

// stackView returns the comment table view mode ("all" or "changed") from
// shared.yaml, defaulting to "all" when unset.
func stackView(s *schemas.Shared) string {
	if s == nil || s.Comments.StackView == "" {
		return render.StackViewAll
	}
	return s.Comments.StackView
}

// ApprovalsConfigFor is the exported extractor used by `reeve rules explain`.
func ApprovalsConfigFor(s *schemas.Shared) approvals.Config { return toApprovalsConfig(s) }

// toApprovalsConfig extracts the approvals config.
func toApprovalsConfig(s *schemas.Shared) approvals.Config {
	if s == nil {
		return approvals.Config{}
	}
	cfg := approvals.Config{Default: toApprovalRule(s.Approvals.Default, nil)}
	// Secure default: dismiss prior approvals when a new commit is pushed
	// unless explicitly disabled. An approval is for the reviewed code, not
	// for whatever is pushed afterward; leaving this off let a reviewer
	// approve a benign commit and an attacker push a malicious one under the
	// same approval.
	if s.Approvals.Default.DismissOnNewCommit == nil {
		cfg.Default.DismissOnNewCommit = true
	}
	// Repo-wide policy: rides on the default rule and is not overridable
	// per stack. Off by default (safe on public repos).
	cfg.Default.AllowUnlistedApprovalsOnPublic = s.Approvals.AllowUnlistedApprovalsOnPublic
	for _, src := range s.Approvals.Sources {
		// Enabled is required by validateApprovalSources (never nil on a
		// loaded config); default a defensive false if a caller bypassed load.
		enabled := src.Enabled != nil && *src.Enabled
		cfg.Sources = append(cfg.Sources, approvals.SourceConfig{
			Type: src.Type, Enabled: enabled, Command: src.Command,
		})
	}
	for pattern, r := range s.Approvals.Stacks {
		present := map[string]bool{}
		if r.RequiredApprovals != nil {
			present["required_approvals"] = true
		}
		if r.Codeowners != nil {
			present["codeowners"] = true
		}
		if r.RequireAllGroups != nil {
			present["require_all_groups"] = true
		}
		if r.DismissOnNewCommit != nil {
			present["dismiss_on_new_commit"] = true
		}
		if r.AllowUnpinnedCommentApprovals != nil {
			present["allow_unpinned_comment_approvals"] = true
		}
		if r.Freshness != "" {
			present["freshness"] = true
		}
		cfg.Stacks = append(cfg.Stacks, approvals.StackRule{
			Pattern: pattern,
			Rules:   toApprovalRule(r, present),
			Present: present,
		})
	}
	return cfg
}

func toApprovalRule(r schemas.ApprovalRuleYAML, present map[string]bool) approvals.Rules {
	out := approvals.Rules{Approvers: r.Approvers}
	if r.RequiredApprovals != nil {
		out.RequiredApprovals = *r.RequiredApprovals
	}
	if r.Codeowners != nil {
		out.Codeowners = *r.Codeowners
	}
	if r.RequireAllGroups != nil {
		out.RequireAllGroups = *r.RequireAllGroups
	}
	if r.DismissOnNewCommit != nil {
		out.DismissOnNewCommit = *r.DismissOnNewCommit
	}
	if r.AllowUnpinnedCommentApprovals != nil {
		out.AllowUnpinnedComments = *r.AllowUnpinnedCommentApprovals
	}
	if r.Freshness != "" {
		if d, err := time.ParseDuration(r.Freshness); err == nil {
			out.Freshness = d
		}
	}
	_ = present
	return out
}

// toBreakGlassConfig extracts the opt-in break_glass block. A nil block
// yields Configured=false, which makes core/breakglass fail closed with a
// polite "not configured" error. override_freeze defaults to TRUE when the
// block exists but the key is omitted.
func toBreakGlassConfig(s *schemas.Shared) breakglass.Config {
	if s == nil || s.BreakGlass == nil {
		return breakglass.Config{}
	}
	bg := s.BreakGlass
	out := breakglass.Config{
		Configured:     true,
		InternalList:   bg.Authorized.InternalList,
		Codeowners:     bg.Authorized.Codeowners,
		Anyone:         bg.Authorized.Anyone,
		VCSBypass:      bg.Authorized.VCSBypass,
		Groups:         bg.Authorized.Groups,
		OverrideFreeze: true,
	}
	if bg.OverrideFreeze != nil {
		out.OverrideFreeze = *bg.OverrideFreeze
	}
	if bg.RejectSelfAuthorization != nil {
		out.RejectSelfAuthorization = *bg.RejectSelfAuthorization
	}
	return out
}

// toFreezeConfig extracts freeze windows.
func toFreezeConfig(s *schemas.Shared) freeze.Config {
	if s == nil {
		return freeze.Config{}
	}
	cfg := freeze.Config{}
	for _, w := range s.FreezeWindows {
		d, _ := time.ParseDuration(w.Duration)
		cfg.Windows = append(cfg.Windows, freeze.Window{
			Name: w.Name, Cron: w.Cron, Duration: d, Stacks: w.Stacks,
		})
	}
	return cfg
}

// LockTTL pulls the configured lock TTL (default 4h). Exported so cmd
// wiring can thread the same TTL into lock-store operations (reap,
// leave, force-unlock) that promote queued holders.
func LockTTL(s *schemas.Shared) time.Duration {
	const def = 4 * time.Hour
	if s == nil || s.Locking.TTL == "" {
		return def
	}
	// A non-positive TTL would produce a born-expired lease with a no-op
	// heartbeat, letting a concurrent run evict the live holder mid-apply.
	// validateDurations rejects that at load, but floor it here too so no
	// path (bypassed validation, direct caller) can disable the lease.
	if d, err := time.ParseDuration(s.Locking.TTL); err == nil && d > 0 {
		return d
	}
	return def
}
