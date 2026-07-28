// Package schemas holds Go structs for each config_type. See
// openspec/specs/config for rules. Phase 1 implements shared + engine
// (pulumi); other config_types scaffold with version+config_type only.
package schemas

// Header is common to every config file.
type Header struct {
	Version    int    `yaml:"version"`
	ConfigType string `yaml:"config_type"`
}

// Shared is .reeve/shared.yaml. Extended in Phase 2 with locking,
// approvals, preconditions, freeze_windows, apply_command.
type Shared struct {
	Header        `yaml:",inline"`
	Bucket        BucketConfig       `yaml:"bucket"`
	Comments      CommentsConfig     `yaml:"comments"`
	Locking       LockingConfig      `yaml:"locking"`
	Approvals     ApprovalsYAML      `yaml:"approvals"`
	Preconditions PreconditionsYAML  `yaml:"preconditions"`
	FreezeWindows []FreezeWindowYAML `yaml:"freeze_windows"`
	BreakGlass    *BreakGlassYAML    `yaml:"break_glass,omitempty"`
	Apply         ApplyConfig        `yaml:"apply"`
	Retention     RetentionConfig    `yaml:"retention"`
	LogLevel      string             `yaml:"log_level"`
	LogFormat     string             `yaml:"log_format"`
}

// RetentionConfig controls opportunistic cleanup of reeve's blob artifacts
// (run manifests, applied-state pointers) under the "runs/" prefix.
type RetentionConfig struct {
	// MaxAge is a Go duration (e.g. "720h"). Blob items older than this are
	// pruned at the start of a run. Empty -> default (DefaultRetentionMaxAge).
	// "0" / negative disables pruning entirely.
	MaxAge string `yaml:"max_age"`
}

// DefaultRetentionMaxAge is one month (30 days), used when retention.max_age
// is unset.
const DefaultRetentionMaxAge = "720h"

type LockingConfig struct {
	TTL            string        `yaml:"ttl"`             // e.g. "4h"
	Queue          string        `yaml:"queue"`           // fifo (v1)
	ReaperInterval string        `yaml:"reaper_interval"` // unused v1 (opportunistic)
	AdminOverride  AdminOverride `yaml:"admin_override"`
}

type AdminOverride struct {
	Allowed        []string `yaml:"allowed" expand:"env"`
	RequiresReason bool     `yaml:"requires_reason"`
}

type ApprovalsYAML struct {
	Sources []ApprovalSource            `yaml:"sources"`
	Default ApprovalRuleYAML            `yaml:"default"`
	Stacks  map[string]ApprovalRuleYAML `yaml:"stacks"`
	// AllowUnlistedApprovalsOnPublic opts a PUBLIC repository into counting
	// approvals from reviewers who are not on an approvers list and not a
	// CODEOWNER. Off by default: on a public repo anyone can submit an
	// approving review, so a bare numeric policy is not a real gate.
	// Ignored on private repos (there the reviewer set is already the
	// collaborator set).
	AllowUnlistedApprovalsOnPublic bool `yaml:"allow_unlisted_approvals_on_public,omitempty"`
}

type ApprovalSource struct {
	Type string `yaml:"type"` // pr_review | pr_comment
	// Enabled is required when the entry is listed: a pointer distinguishes
	// "omitted" (nil → rejected at load, so a listed source is never silently
	// off) from an explicit true/false. There is no safe default — omitting it
	// on pr_review used to disable reviews, the opposite of the usual intent.
	Enabled *bool  `yaml:"enabled"`
	Command string `yaml:"command"` // for pr_comment
}

type ApprovalRuleYAML struct {
	RequiredApprovals  *int     `yaml:"required_approvals,omitempty"`
	Approvers          []string `yaml:"approvers"`
	Codeowners         *bool    `yaml:"codeowners,omitempty"`
	RequireAllGroups   *bool    `yaml:"require_all_groups,omitempty"`
	DismissOnNewCommit *bool    `yaml:"dismiss_on_new_commit,omitempty"`
	Freshness          string   `yaml:"freshness,omitempty"` // e.g. "24h"
	// AllowUnpinnedCommentApprovals opts into counting a bare `/reeve approve`
	// (a pr_comment approval that names no SHA) even when dismiss_on_new_commit
	// is on. Off by default: an unnamed comment approval cannot be bound to the
	// reviewed commit, so the secure default drops it and asks the reviewer to
	// name the SHA (`/reeve approve <sha>`). Set true to accept bare approvals
	// as approve-and-stick (they survive new commits) - a deliberate weaker
	// policy for teams that trust their allowed approvers.
	AllowUnpinnedCommentApprovals *bool `yaml:"allow_unpinned_comment_approvals,omitempty"`
}

type PreconditionsYAML struct {
	RequireUpToDate         *bool  `yaml:"require_up_to_date,omitempty"`
	RequireChecksPassing    *bool  `yaml:"require_checks_passing,omitempty"`
	PreviewFreshness        string `yaml:"preview_freshness,omitempty"` // e.g. "2h"
	PreviewMaxCommitsBehind int    `yaml:"preview_max_commits_behind"`
}

// BreakGlassYAML is the opt-in `break_glass:` block in shared.yaml.
// Absent (nil) means break-glass is OFF: the `/reeve breakglass` command
// errors politely. Authorization is resolved from the config as of the PR
// HEAD (self-add is allowed by design; the audit record flags same-PR
// modification of the authorizing files).
type BreakGlassYAML struct {
	Authorized BreakGlassAuthorized `yaml:"authorized"`
	// OverrideFreeze: break-glass also overrides freeze windows. nil
	// defaults to TRUE - an emergency override that stops at a scheduled
	// freeze is not much of an override. Set false to keep freeze binding.
	OverrideFreeze *bool `yaml:"override_freeze,omitempty"`
	// RejectSelfAuthorization locks down the head-resolved authorization:
	// when true, a PR that modifies its own authorizing files (a .reeve
	// config or CODEOWNERS) cannot authorize a break-glass apply, no matter
	// which source would grant. Default false keeps the documented
	// behavior — self-add is allowed but loudly audited — for operators who
	// prioritize late-night availability; set true when you would rather
	// fail closed than allow same-PR self-authorization.
	RejectSelfAuthorization *bool `yaml:"reject_self_authorization,omitempty"`
}

// BreakGlassAuthorized is the union of authorization sources - any source
// granting the actor is enough.
type BreakGlassAuthorized struct {
	// InternalList holds explicit logins and "org/team" slugs.
	InternalList []string `yaml:"internal_list,omitempty"`
	// Codeowners: anyone CODEOWNERS makes an owner of a changed path.
	Codeowners bool `yaml:"codeowners,omitempty"`
	// Anyone: any actor may break-glass (justification + audit still apply).
	Anyone bool `yaml:"anyone,omitempty"`
	// VCSBypass: GitHub ruleset bypass actors. Config surface only - the
	// runtime rejects it with a clear "not yet supported" error.
	VCSBypass bool `yaml:"vcs_bypass,omitempty"`
	// Groups holds phase-2 external identity group sources
	// ("group:<provider>:<name>"). Parsed and rejected until phase 2.
	Groups []string `yaml:"groups,omitempty"`
}

type FreezeWindowYAML struct {
	Name     string   `yaml:"name"`
	Cron     string   `yaml:"cron"`
	Duration string   `yaml:"duration"` // e.g. "65h"
	Stacks   []string `yaml:"stacks"`
}

// Apply trigger modes. The trigger selects exactly ONE apply-initiation
// path; it is a flow selector, never a gate, and never weakens approvals,
// locks, freeze, checks, or preview freshness.
const (
	// ApplyTriggerComment applies only from a /reeve apply (or /reeve up)
	// PR comment. This is the default (apply-then-merge flow).
	ApplyTriggerComment = "comment"
	// ApplyTriggerMerge applies automatically when the PR is merged
	// (merge-then-apply / continuous-delivery flow). Comment-initiated
	// applies are rejected as a no-op in this mode.
	ApplyTriggerMerge = "merge"
)

type ApplyConfig struct {
	// Trigger selects the apply-initiation path: "comment" (default) |
	// "merge". See the ApplyTrigger* constants and TriggerMode().
	Trigger string `yaml:"trigger"`
	// AllowForkPRs: if true, apply runs on fork PRs with full creds.
	// Default false. Surfaces via preconditions GateFork.
	AllowForkPRs bool `yaml:"allow_fork_prs"`
	// AutoReady: if true, reeve automatically marks the PR ready (posts
	// comment + Slack notification) after a fully successful plan, and
	// also when the PR transitions from draft to ready_for_review.
	AutoReady bool `yaml:"auto_ready"`
}

// TriggerMode returns the resolved apply trigger mode, defaulting to
// "comment" when unset. An unrecognized value is validated (and rejected)
// by config.Validate; this accessor treats anything other than the exact
// "merge" keyword as the safe comment default.
func (a ApplyConfig) TriggerMode() string {
	if a.Trigger == ApplyTriggerMerge {
		return ApplyTriggerMerge
	}
	return ApplyTriggerComment
}

// BucketConfig fields carry `expand:"env"`: they are part of the enumerated
// env-expansion allow-list (see internal/config/env_expand.go and
// docs/configuration.md#token-expansion).
type BucketConfig struct {
	Type   string `yaml:"type"`                // filesystem | s3 | gcs | azblob | r2
	Name   string `yaml:"name" expand:"env"`   // bucket name, or directory for filesystem
	Region string `yaml:"region" expand:"env"` // optional
	Prefix string `yaml:"prefix" expand:"env"` // optional sub-prefix
}

type CommentsConfig struct {
	Sort              string `yaml:"sort"`               // status_grouped | alphabetical | env_priority
	CollapseThreshold int    `yaml:"collapse_threshold"` // collapse no-op stacks above N
	ShowGates         bool   `yaml:"show_gates"`
	Style             string `yaml:"style"`      // replace (default) | append | section
	StackView         string `yaml:"stack_view"` // all (default) | changed — whether to list no-op stacks in the table
}

// Engine is .reeve/<engine>.yaml. Phase 1 supports Pulumi stack declarations
// and basic filters. Full surface (modules, stack_dependencies,
// policy_hooks, stack_overrides) lands in later phases.
type Engine struct {
	Header `yaml:",inline"`
	Engine EngineBody `yaml:"engine"`
}

type EngineBody struct {
	Type          string           `yaml:"type"` // pulumi | terraform | tofu (OpenTofu)
	Binary        EngineBinary     `yaml:"binary"`
	State         EngineState      `yaml:"state,omitempty"`
	Stacks        []StackDecl      `yaml:"stacks"`
	Filters       EngineFilters    `yaml:"filters"`
	ChangeMapping ChangeMap        `yaml:"change_mapping"`
	Execution     Execution        `yaml:"execution"`
	PolicyHooks   []PolicyHookYAML `yaml:"policy_hooks,omitempty"`
}

// EngineState describes the engine's own state backend. reeve does not
// manage state - it configures the engine (e.g. `pulumi login`) before
// each run.
type EngineState struct {
	Backend         string                 `yaml:"backend"` // s3 | gcs | azblob | pulumi_cloud | file
	URL             string                 `yaml:"url,omitempty"`
	AuthProvider    string                 `yaml:"auth_provider,omitempty"` // refers to auth.yaml
	SecretsProvider EngineSecretsProvider  `yaml:"secrets_provider,omitempty"`
	StackOverrides  map[string]EngineState `yaml:"stack_overrides,omitempty"`
}

// EngineSecretsProvider is the engine's secret-encryption backend (Pulumi's
// awskms / gcpkms / passphrase / etc.). Separate from auth.yaml runtime
// creds - this encrypts Pulumi stack state at rest.
type EngineSecretsProvider struct {
	Type       string `yaml:"type"` // awskms | gcpkms | azurekeyvault | hashivault | passphrase
	Key        string `yaml:"key,omitempty"`
	Passphrase string `yaml:"passphrase,omitempty"`
}

// PolicyHookYAML is one entry in engine.policy_hooks.
type PolicyHookYAML struct {
	Name    string   `yaml:"name"`
	Command []string `yaml:"command"`
	OnFail  string   `yaml:"on_fail"` // block | warn (default block)
	// Required controls what happens when command[0] is not on PATH.
	// nil (omitted) defaults to TRUE - a missing scanner binary fails the
	// run instead of silently skipping the policy gate. Set
	// `required: false` to explicitly opt in to the silent skip.
	Required *bool `yaml:"required,omitempty"`
}

// IsRequired resolves the fail-closed default: an omitted `required:` means
// the hook is required.
func (h PolicyHookYAML) IsRequired() bool { return h.Required == nil || *h.Required }

type EngineBinary struct {
	Path    string `yaml:"path"`
	Version string `yaml:"version"` // optional pin
}

// StackDecl is either a literal project entry or a pattern.
type StackDecl struct {
	Project string   `yaml:"project"` // literal
	Path    string   `yaml:"path"`    // literal
	Pattern string   `yaml:"pattern"` // glob (re: prefix not supported in v1)
	Stacks  []string `yaml:"stacks"`  // which stack names apply
}

type EngineFilters struct {
	Exclude []ExcludeRule `yaml:"exclude"`
}

// ExcludeRule accepts either a plain string pattern or a {stack: pattern}
// form for stack-level filtering.
type ExcludeRule struct {
	Pattern string `yaml:"-"`
	Stack   string `yaml:"stack"`
}

type ChangeMap struct {
	IgnoreChanges []string       `yaml:"ignore_changes"`
	ExtraTriggers []ExtraTrigger `yaml:"extra_triggers"`
	// Scope controls behavior when a changed file maps to no specific stack:
	//   auto (default) - Pulumi-relevant source outside any stack dir previews
	//                    every declared stack.
	//   pulumi_only    - act only on files inside a stack dir (Pulumi.yaml
	//                    present); never broaden to all stacks.
	Scope string `yaml:"scope"`
}

// Change-mapping scope values.
const (
	ScopeAuto       = "auto" // default
	ScopePulumiOnly = "pulumi_only"
)

type ExtraTrigger struct {
	Project string   `yaml:"project"`
	Paths   []string `yaml:"paths"`
}

type Execution struct {
	MaxParallelStacks int    `yaml:"max_parallel_stacks"`
	PreviewTimeout    string `yaml:"preview_timeout"` // e.g. "10m"
	ApplyTimeout      string `yaml:"apply_timeout"`
}
