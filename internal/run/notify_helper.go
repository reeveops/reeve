package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/notify"
)

// PulumiLogin runs `pulumi login <backendURL>` if a backend URL is configured.
func PulumiLogin(ctx context.Context, cfg *schemas.Engine) error {
	if cfg == nil || cfg.Engine.State.URL == "" {
		return nil
	}
	binary := "pulumi"
	if cfg.Engine.Binary.Path != "" {
		binary = cfg.Engine.Binary.Path
	}
	// #nosec G204 -- binary is engine.binary.path from operator config; `login` and the backend
	// URL are separate argv elements, never a shell string
	cmd := exec.CommandContext(ctx, binary, "login", cfg.Engine.State.URL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulumi login %s: %w", cfg.Engine.State.URL, err)
	}
	return nil
}

// BuildNotifyChannels resolves the configured notification channels
// (the `channels:` list) through the notify registry. Returns nil when
// nothing is configured. Build errors are logged, not fatal -
// notifications never abort a run. comments backs the timeline_github
// channel and may be nil (the channel is then skipped).
func BuildNotifyChannels(ctx context.Context, cfg *schemas.Notifications, store blob.Store, comments notify.CommentClient) []notify.Channel {
	var cfgs []schemas.ChannelYAML
	if cfg != nil {
		cfgs = cfg.Channels
	}
	if len(cfgs) == 0 {
		return nil
	}
	channels, err := notify.Build(ctx, cfgs, notify.Deps{
		Blob:       store,
		Comments:   comments,
		SlackToken: os.Getenv("SLACK_BOT_TOKEN"),
		RepoFull:   os.Getenv("GITHUB_REPOSITORY"),
	})
	if err != nil {
		slog.Warn("notification channel build failed", "err", err)
	}
	return channels
}

// prNotifyClient is the subset of the VCS client BuildPRNotifyChannels needs:
// a PR's changed files (for the suppression check) and the comment client that
// backs the timeline_github channel. *github.Client satisfies both.
type prNotifyClient interface {
	ListChangedFiles(ctx context.Context, number int) ([]string, error)
	notify.CommentClient
}

// BuildPRNotifyChannels builds notification channels for a PR-flow event while
// enforcing the SAME pre-approval suppression the preview path applies.
//
// approved/ready run on the untrusted PR HEAD checkout, so channel config
// (webhook URLs, headers, and their expanded ${env:...} credentials) is
// attacker-controlled when the PR modifies the channel-bearing files. In that
// case - or when the changed-file list can't be fetched - no channels are
// returned and reason explains why; the caller simply skips its notification.
// A PR that does not touch notification config notifies normally.
func BuildPRNotifyChannels(ctx context.Context, cfg *schemas.Notifications, channelSourceFiles []string, store blob.Store, client prNotifyClient, pr int) (channels []notify.Channel, reason string) {
	changed, changedErr := client.ListChangedFiles(ctx, pr)
	if suppress, why := SuppressPreApprovalChannels(false, true, changed, changedErr, channelSourceFiles); suppress {
		return nil, why
	}
	return BuildNotifyChannels(ctx, cfg, store, client), ""
}

// defaultChannelSourceFiles is the fail-closed fallback when the caller did
// not record which config files declared notification channels.
var defaultChannelSourceFiles = []string{".reeve/notifications.yaml", ".reeve/drift.yaml"}

// defaultObservabilitySourceFiles is the equivalent fallback for the
// observability (OTEL exporter) config.
var defaultObservabilitySourceFiles = []string{".reeve/observability.yaml"}

// SuppressPreApprovalChannels decides whether pre-approval events (the
// preview path's planning/plan) may be dispatched to notification channels.
//
// Previews run automatically on the untrusted PR HEAD before any approval,
// and channel config (webhook URLs, headers) is loaded from that HEAD. If
// the PR modifies the channel-bearing config files, a branch pusher could
// point a channel at an attacker-controlled endpoint and exfiltrate
// expanded credentials - so dispatch is suppressed entirely. The
// ready/approved paths run on the same untrusted HEAD and apply this gate
// too, via BuildPRNotifyChannels.
//
// Fail closed: no VCS client or an unavailable changed-file list in a
// non-local run also suppresses. --local runs never suppress (no VCS;
// notifications are already local-only behavior).
//
// Returns (suppress, reason).
func SuppressPreApprovalChannels(local, hasVCS bool, changed []string, changedErr error, sourceFiles []string) (bool, string) {
	return suppressPreApprovalConfig(local, hasVCS, changed, changedErr,
		sourceFiles, defaultChannelSourceFiles, "notification config")
}

// SuppressPreApprovalObservability is the same gate for the OTEL exporter:
// observability.yaml is loaded from the PR HEAD too, and its endpoint +
// headers are designated ${env:} fields - a PR-added collector config
// would exfiltrate expanded credentials with the first pre-approval span
// flush. Identical fail-closed semantics to the channel gate.
func SuppressPreApprovalObservability(local, hasVCS bool, changed []string, changedErr error, sourceFiles []string) (bool, string) {
	return suppressPreApprovalConfig(local, hasVCS, changed, changedErr,
		sourceFiles, defaultObservabilitySourceFiles, "observability config")
}

func suppressPreApprovalConfig(local, hasVCS bool, changed []string, changedErr error, sourceFiles, defaults []string, label string) (bool, string) {
	if local {
		return false, ""
	}
	if !hasVCS {
		return true, "changed-file list unavailable (no VCS client)"
	}
	if changedErr != nil {
		return true, "changed-file list unavailable (" + changedErr.Error() + ")"
	}
	if len(sourceFiles) == 0 {
		sourceFiles = defaults
	}
	for _, f := range changed {
		norm := filepath.ToSlash(f)
		for _, src := range sourceFiles {
			if norm == filepath.ToSlash(src) {
				return true, fmt.Sprintf("%s %s modified in this PR", label, src)
			}
		}
	}
	return false, ""
}

// PRNotifyInput bundles the PR-flow event context.
type PRNotifyInput struct {
	PR                int
	CommitSHA         string
	RunURL            string
	PRTitle           string
	PRAuthor          string
	RequiredApprovers []string
	Stacks            []summary.StackSummary
}

// NotifyPREvent publishes one PR-flow event to the configured channels. The
// PR-flow producer runs last in the pipeline so upstream failures are
// captured accurately; a delivery failure is returned (joined) for the
// caller to log, never to abort on.
func NotifyPREvent(ctx context.Context, channels []notify.Channel, ev notify.Event, in PRNotifyInput) error {
	if len(channels) == 0 {
		return nil
	}
	payload := notify.Payload{
		Event: ev,
		PR: &notify.PRPayload{
			PR:                in.PR,
			CommitSHA:         in.CommitSHA,
			RunURL:            in.RunURL,
			Title:             in.PRTitle,
			Author:            in.PRAuthor,
			RepoFull:          os.Getenv("GITHUB_REPOSITORY"),
			RequiredApprovers: in.RequiredApprovers,
			Stacks:            toStackResults(in.Stacks),
		},
	}
	return errors.Join(notify.Dispatch(ctx, channels, []notify.Payload{payload})...)
}

// ApplyOutcomeEvent picks the terminal apply event the same way the legacy
// Slack backend did: errors win over blocked, blocked over applied.
func ApplyOutcomeEvent(stacks []summary.StackSummary, blocked bool) notify.Event {
	for _, s := range stacks {
		if s.Status == summary.StatusError {
			return notify.EventFailed
		}
	}
	if blocked {
		return notify.EventBlocked
	}
	return notify.EventApplied
}

func toStackResults(ss []summary.StackSummary) []notify.StackResult {
	out := make([]notify.StackResult, 0, len(ss))
	for _, s := range ss {
		out = append(out, notify.StackResult{
			Project: s.Project,
			Stack:   s.Stack,
			Env:     s.Env,
			Status:  string(s.Status),
			Add:     s.Counts.Add,
			Change:  s.Counts.Change,
			Delete:  s.Counts.Delete,
			Replace: s.Counts.Replace,
		})
	}
	return out
}
