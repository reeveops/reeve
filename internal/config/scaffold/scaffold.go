// Package scaffold renders the sane-default .reeve/ configuration written by
// `reeve init`. Rendering is pure (no disk I/O, no prompts) so both the
// interactive wizard and the non-interactive path share one generator, and
// tests can round-trip every permutation through the strict loader.
package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/core/discovery"
)

// Approval-mode values for Options.ApprovalMode.
const (
	// ApprovalBaseline writes required_approvals: 1 with commented
	// approvers/codeowners placeholders - the non-interactive default.
	ApprovalBaseline = ""
	// ApprovalCodeowners derives approvers from CODEOWNERS.
	ApprovalCodeowners = "codeowners"
	// ApprovalApprovers writes an explicit approver list.
	ApprovalApprovers = "approvers"
)

// Options selects what `reeve init` writes. The zero value is the safe
// baseline: pulumi engine, no stacks pre-filled, every optional gate off.
type Options struct {
	// EngineType is the IaC engine: "pulumi", "terraform", or "tofu"
	// (OpenTofu). Empty defaults to pulumi.
	EngineType string
	// Stacks pre-fills engine.stacks from the discovery scan. Empty writes
	// stacks: [] plus a pointer at `reeve stacks discover --write`.
	Stacks []discovery.Declaration

	// ApprovalMode is one of ApprovalBaseline, ApprovalCodeowners,
	// ApprovalApprovers.
	ApprovalMode string
	// Approvers is the explicit approver list (ApprovalApprovers mode).
	Approvers []string
	// RequiredApprovals for the default rule; 0 means 1.
	RequiredApprovals int
	// Freshness, when set (a Go duration like "24h"), writes
	// approvals.default.freshness so approvals expire after the window.
	Freshness string

	// FreezeWindowExample writes a commented (disabled) example freeze
	// window instead of the bare freeze_windows pointer comment.
	FreezeWindowExample bool

	// SlackChannel, when set, writes notifications.yaml with a v2 `channels:`
	// slack entry for the channel. Empty writes no notifications file.
	SlackChannel string
}

// File is one rendered config file, not yet written to disk.
type File struct {
	// Name is the file name inside .reeve/ (e.g. "shared.yaml").
	Name string
	// ConfigType is the file's config_type header value, used for the
	// fill-only-missing check against an existing .reeve/.
	ConfigType string
	Content    []byte
}

// Validate rejects option combinations that would render broken config.
func (o Options) Validate() error {
	switch o.EngineType {
	case "", "pulumi", "terraform", "tofu":
	default:
		return fmt.Errorf("unknown engine %q (pulumi | terraform | tofu)", o.EngineType)
	}
	switch o.ApprovalMode {
	case ApprovalBaseline, ApprovalCodeowners:
	case ApprovalApprovers:
		if len(o.Approvers) == 0 {
			return fmt.Errorf("approval mode %q requires at least one approver", o.ApprovalMode)
		}
	default:
		return fmt.Errorf("unknown approval mode %q", o.ApprovalMode)
	}
	if o.RequiredApprovals < 0 {
		return fmt.Errorf("required approvals must be >= 0, got %d", o.RequiredApprovals)
	}
	if o.Freshness != "" {
		if _, err := time.ParseDuration(o.Freshness); err != nil {
			return fmt.Errorf("freshness %q is not a Go duration (e.g. 24h): %w", o.Freshness, err)
		}
	}
	return nil
}

// Render produces the config files for opts: shared.yaml, <engine>.yaml, and
// (only when a Slack channel is set) notifications.yaml. Every file passes
// the strict loader; see the package tests for the round-trip guarantee.
func Render(opts Options) ([]File, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if opts.EngineType == "" {
		opts.EngineType = "pulumi"
	}
	if opts.RequiredApprovals == 0 {
		opts.RequiredApprovals = 1
	}

	engineYAML, err := renderEngine(opts)
	if err != nil {
		return nil, err
	}
	files := []File{
		{Name: "shared.yaml", ConfigType: "shared", Content: renderShared(opts)},
		{Name: opts.EngineType + ".yaml", ConfigType: "engine", Content: engineYAML},
	}
	if opts.SlackChannel != "" {
		files = append(files, File{
			Name: "notifications.yaml", ConfigType: "notifications",
			Content: renderNotifications(opts),
		})
	}
	return files, nil
}

func renderShared(opts Options) []byte {
	var b strings.Builder
	b.WriteString(`version: 1
config_type: shared

# Where reeve keeps its own state: locks, run artifacts, audit entries.
# filesystem is perfect for a first run - but state resets whenever CI starts
# fresh, so switch to a real bucket (s3 | gcs | azblob | r2) before enabling
# apply, so locks survive between runs.
bucket:
  type: filesystem
  name: ./.reeve-state
  # type: s3
  # name: mycompany-reeve
  # region: us-east-1

# How the PR comment is rendered.
comments:
  sort: status_grouped
  collapse_threshold: 10
  show_gates: true

# Per-stack FIFO locks around apply.
locking:
  ttl: 4h
  queue: fifo

# Who may approve an apply, and how many approvals it takes.
# Inspect the merged result per stack with: reeve rules explain <stack>
approvals:
  sources:
    - type: pr_review
      enabled: true
  default:
`)
	fmt.Fprintf(&b, "    required_approvals: %d\n", opts.RequiredApprovals)
	switch opts.ApprovalMode {
	case ApprovalCodeowners:
		b.WriteString("    codeowners: true            # approvers derive from CODEOWNERS entries\n")
	case ApprovalApprovers:
		b.WriteString("    approvers: [" + quoteList(opts.Approvers) + "]\n")
	default:
		b.WriteString(`    # approvers: ["@your-org/infra-reviewers"]  # explicit approver teams/users
    # codeowners: true                          # or derive from CODEOWNERS
`)
	}
	b.WriteString("    dismiss_on_new_commit: true\n")
	if opts.Freshness != "" {
		fmt.Fprintf(&b, "    freshness: %s              # approvals older than this go stale\n", opts.Freshness)
	} else {
		b.WriteString("    # freshness: 24h            # optionally expire approvals after a window\n")
	}
	b.WriteString(`  # Per-stack overrides - e.g. two approvals for anything named prod:
  # stacks:
  #   "*/prod":
  #     required_approvals: 2

# Safety gates checked before apply.
preconditions:
  require_up_to_date: true
  require_checks_passing: true
  preview_freshness: 2h
  preview_max_commits_behind: 5

`)
	if opts.FreezeWindowExample {
		b.WriteString(`# Freeze windows block apply while active. This example freezes prod stacks
# every Friday 15:00 for 65h (through Monday morning). It is disabled while
# commented - uncomment to enable, then verify with: reeve lint
# freeze_windows:
#   - name: weekend-freeze
#     cron: "0 15 * * 5"
#     duration: 65h
#     stacks: ["*/prod"]
`)
	} else {
		b.WriteString("# freeze_windows: []          # block apply during change freezes - see docs/configuration.md\n")
	}
	b.WriteString(`
apply:
  trigger: comment            # comment (default): apply on /reeve apply | merge: apply on PR merge
  allow_fork_prs: false       # fork PRs stay dry-run only
  # auto_ready: true          # notify for approval when a draft PR becomes ready

# Break-glass: authorize an emergency apply that bypasses gates, with a
# recorded reason and audit trail. Off unless configured. Example:
# break_glass:
#   authorized:
#     internal_list: ["your-org/sre"]
#     codeowners: true
#   override_freeze: true
`)
	return []byte(b.String())
}

func renderEngine(opts Options) ([]byte, error) {
	var base []byte
	switch opts.EngineType {
	case "terraform", "tofu":
		binary := opts.EngineType // terraform | tofu
		base = []byte(`version: 1
config_type: engine

engine:
  type: ` + opts.EngineType + `
  binary:
    path: ` + binary + `              # resolved on $PATH; pin with version below
    # version: 1.9.8

  # Declared stacks. A root-module directory is a project; a workspace is a
  # stack. Dir-per-env layouts use the default workspace per directory:
  #   - pattern: "envs/*"
  #     stacks: [default]
  # Declared stacks are authoritative (no workspace enumeration needed).
  # Regenerate this block any time with:
  #   reeve stacks discover --engine ` + opts.EngineType + ` --write
  stacks: []

  filters:
    exclude: []

  change_mapping:
    # Files that never trigger a run (docs/images are skipped by default).
    ignore_changes:
      - "**/.terraform/**"

  execution:
    max_parallel_stacks: 2
    preview_timeout: 10m
    apply_timeout: 30m
`)
	default: // pulumi
		base = []byte(`version: 1
config_type: engine

engine:
  type: pulumi
  binary:
    path: pulumi              # resolved on $PATH; pin with version below
    # version: 3.231.0

  # Declared stacks. Globs are doublestar patterns over project paths, e.g.
  #   - pattern: "projects/*"
  #     stacks: [dev, prod]
  # Regenerate this block any time with: reeve stacks discover --write
  stacks: []

  filters:
    exclude: []

  change_mapping:
    # Files that never trigger a run (docs/images are skipped by default).
    ignore_changes:
      - "**/node_modules/**"
    # scope: pulumi_only      # only act on files inside a stack directory

  execution:
    max_parallel_stacks: 2
    preview_timeout: 10m
    apply_timeout: 30m
`)
	}
	if len(opts.Stacks) == 0 {
		return base, nil
	}
	// Reuse the `stacks discover --write` writer so init pre-fills the exact
	// same shape, with the template's comments preserved.
	return config.ClusteredStacksBytes(base, opts.Stacks)
}

func renderNotifications(opts Options) []byte {
	var b strings.Builder
	b.WriteString(`version: 2
config_type: notifications

# Generic notification channels. Each channel subscribes to lifecycle events via
# on: - valid events are plan, ready, approved, applying, applied, failed,
# blocked (drift_* events are for drift.yaml channels). Details:
# docs/notifications.md
channels:
  - type: slack
`)
	fmt.Fprintf(&b, "    channel: %q\n", opts.SlackChannel)
	b.WriteString(`    auth_token: "${env:SLACK_BOT_TOKEN}"  # set SLACK_BOT_TOKEN in CI secrets
    trigger: apply     # create the message when apply starts; edit in place after
    # Restrict which events post, e.g. only final outcomes:
    # on: [applied, failed]
`)
	return []byte(b.String())
}

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, it := range items {
		quoted = append(quoted, fmt.Sprintf("%q", it))
	}
	return strings.Join(quoted, ", ")
}

// ExistingTypes maps config_type -> file name for every parseable
// .reeve/*.yaml in dir. A missing dir returns an empty map (not an error) so
// callers can treat "no .reeve/ yet" and "empty .reeve/" identically. Files
// without a config_type header are reported under their own name so init
// never silently overwrites something it does not understand.
func ExistingTypes(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	// Scoped to dir for the same reason as the config loader: entries here
	// may be symlinks committed by a PR, and a DirEntry name being
	// separator-free says nothing about where the link points.
	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer dirRoot.Close()

	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		data, err := dirRoot.ReadFile(n)
		if err != nil {
			return nil, err
		}
		var hdr struct {
			ConfigType string `yaml:"config_type"`
		}
		key := "file:" + n // unparseable/headerless: keyed by name, still counts as present
		if yaml.Unmarshal(data, &hdr) == nil && hdr.ConfigType != "" {
			key = hdr.ConfigType
		}
		if _, dup := out[key]; !dup {
			out[key] = n
		}
	}
	return out, nil
}
