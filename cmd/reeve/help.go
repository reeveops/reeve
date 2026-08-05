package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/blob/factory"
	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/core/render"
	gh "github.com/FynxLabs/reeve/internal/vcs/github"
)

func newHelpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr-help",
		Short: "Post a help comment to the PR listing available reeve commands",
		RunE:  runHelp,
	}
	cmd.Flags().Int("pr", 0, "PR number")
	cmd.Flags().String("repo", "", "owner/repo (default: $GITHUB_REPOSITORY)")
	cmd.Flags().String("token", "", "GitHub token (default: $GITHUB_TOKEN)")
	return cmd
}

func runHelp(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	pr := flagInt(cmd, "pr")
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
		return fmt.Errorf("help requires --repo (or $GITHUB_REPOSITORY) and --token (or $GITHUB_TOKEN)")
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
	_ = store

	body := render.BuildHelpComment(cfg.Shared.Apply.AutoReady)

	if pr > 0 {
		if err := client.UpsertComment(ctx, pr, body, "<!-- reeve:help -->"); err != nil {
			return fmt.Errorf("post help comment: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "posted help comment on PR #%d\n", pr)
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), body)
	}
	return nil
}
