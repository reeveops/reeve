package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/blob/factory"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/run"
	gh "github.com/reeveops/reeve/internal/vcs/github"
)

func newExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Report-only: post the why - approval rules, lock state, and a full gate trace - without running anything",
		RunE:  runExplain,
	}
	cmd.Flags().Int("pr", 0, "PR number")
	cmd.Flags().String("stack", "", "Limit to one project/stack ref")
	cmd.Flags().String("sha", "", "Commit SHA (default: $GITHUB_SHA)")
	cmd.Flags().String("run-url", "", "CI run URL")
	cmd.Flags().String("repo", "", "owner/repo (default: $GITHUB_REPOSITORY)")
	cmd.Flags().String("token", "", "GitHub token (default: $GITHUB_TOKEN)")
	cmd.Flags().String("root", "", "Repo root (default: cwd)")
	return cmd
}

func runExplain(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	pr := flagInt(cmd, "pr")
	sha := flagStringOrEnv(cmd, "sha", "GITHUB_SHA")
	runURL := flagStringOrEnv(cmd, "run-url", "")
	repoFull := flagStringOrEnv(cmd, "repo", "GITHUB_REPOSITORY")
	token := flagStringOrEnv(cmd, "token", "GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("REEVE_GITHUB_TOKEN")
	}
	ciRunID, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	root := flagStringOrDefault(cmd, "root", "")
	if root == "" {
		root, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(root)
	root = abs

	if pr == 0 || repoFull == "" || token == "" {
		return fmt.Errorf("explain requires --pr, --repo (or $GITHUB_REPOSITORY), and --token (or $GITHUB_TOKEN)")
	}
	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid --repo %q: want owner/name", repoFull)
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

	engineCfg := cfg.Engines[0]
	engine, err := iac.New(engineCfg.Engine)
	if err != nil {
		return err
	}

	client, err := gh.New(ctx, token, parts[0], parts[1])
	if err != nil {
		return err
	}

	out, err := run.Explain(ctx, run.ExplainInput{
		PRNumber:       pr,
		CommitSHA:      sha,
		CIRunID:        ciRunID,
		CIRunURL:       runURL,
		SelfCheckNames: selfCheckNames(),
		RepoRoot:       root,
		Engine:         engine,
		Config:         engineCfg,
		Shared:         cfg.Shared,
		Blob:           store,
		Locks:          blocks.New(store),
		VCS:            client,
		StackFilter:    flagStringOrDefault(cmd, "stack", ""),
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), out.Body)
	return nil
}
