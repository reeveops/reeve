package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/blob/factory"
	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/run"
	gh "github.com/FynxLabs/reeve/internal/vcs/github"
)

func newApprovedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approved",
		Short: "Update Slack to approved state when a PR review is submitted",
		RunE:  runApproved,
	}
	cmd.Flags().Int("pr", 0, "PR number (required)")
	cmd.Flags().String("sha", "", "Commit SHA (default: $GITHUB_SHA)")
	cmd.Flags().String("run-url", "", "CI run URL")
	cmd.Flags().String("repo", "", "owner/repo (default: $GITHUB_REPOSITORY)")
	cmd.Flags().String("token", "", "GitHub token (default: $GITHUB_TOKEN)")
	cmd.Flags().String("root", "", "Repo root (default: cwd)")
	_ = cmd.MarkFlagRequired("pr")
	return cmd
}

func runApproved(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	pr := flagInt(cmd, "pr")
	sha := flagStringOrEnv(cmd, "sha", "GITHUB_SHA")
	runURL := flagStringOrEnv(cmd, "run-url", "")
	repoFull := flagStringOrEnv(cmd, "repo", "GITHUB_REPOSITORY")
	token := flagStringOrEnv(cmd, "token", "GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("REEVE_GITHUB_TOKEN")
	}
	root := flagStringOrDefault(cmd, "root", "")
	if root == "" {
		root, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(root)
	root = abs

	if repoFull == "" || token == "" {
		return fmt.Errorf("approved requires --repo (or $GITHUB_REPOSITORY) and --token (or $GITHUB_TOKEN)")
	}

	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid --repo %q (want owner/name)", repoFull)
	}
	client, err := gh.New(ctx, token, parts[0], parts[1])
	if err != nil {
		return err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	applyLogConfig(cfg.LogSettings())
	if err := cfg.Validate(); err != nil {
		return err
	}

	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return err
	}

	prMeta, err := client.GetPR(ctx, pr)
	if err != nil {
		return fmt.Errorf("get pr: %w", err)
	}
	if prMeta.HeadSHA != "" {
		sha = prMeta.HeadSHA
	}

	channels, reason := run.BuildPRNotifyChannels(ctx, cfg.Notifications, cfg.ChannelSourceFiles, store, client, pr)
	if reason != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "notify: channels suppressed (%s)\n", reason)
	}
	if err := run.NotifyPREvent(ctx, channels, notify.EventApproved, run.PRNotifyInput{
		PR: pr, CommitSHA: sha, RunURL: runURL,
		PRTitle: prMeta.Title, PRAuthor: prMeta.Author,
	}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "notify: %v\n", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "PR #%d approved notification sent\n", pr)
	return nil
}
