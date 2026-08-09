package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/audit"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/run"
	gh "github.com/reeveops/reeve/internal/vcs/github"
)

func newRefreshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Reconcile engine state with live infrastructure",
		Long: `Reconcile the engine's recorded state with what the providers actually
report (` + "`pulumi refresh`" + ` / ` + "`terraform apply -refresh-only`" + `).

This changes NO infrastructure. It changes reeve's - and your engine's -
record of it. A "delete" in the output means the resource was already gone
from the provider and has been dropped from state.

Use it when a plan looks wrong because state is stale: someone changed a
resource in the console, an apply died halfway, a resource was deleted out
of band. After a refresh, re-run ` + "`/reeve preview`" + ` and the diff is
against reality.

Gates: fork-PR policy, draft PRs, freeze windows, and per-stack locks are
enforced. Approvals, required checks, and preview freshness are not - those
gate a change set, and a refresh has none. Every stack is refreshed under
its own lock, and the run is written to the audit log.

Scope defaults to the stacks this PR's changed files map to. Use --all to
refresh every declared stack; state drifts for reasons that have nothing to
do with the PR.

Exit codes:
  0  every stack refreshed cleanly, or was blocked by a lock/freeze
  1  one or more stacks failed to refresh`,
		RunE: runRefresh,
	}
	addPreviewFlags(cmd)
	cmd.Flags().String("actor", "", "User triggering the refresh (default: $GITHUB_ACTOR)")
	cmd.Flags().Bool("dry-run", false,
		"Report what a refresh would reconcile and write no state. Takes no locks - it is a read.")
	cmd.Flags().Bool("all", false,
		"Refresh every declared stack instead of the ones this PR's changed files map to")
	return cmd
}

func runRefresh(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	local := flagBool(cmd, "local")
	pr := flagInt(cmd, "pr")
	sha := flagStringOrEnv(cmd, "sha", "GITHUB_SHA")
	runNum := flagIntOrEnv(cmd, "run-number", "GITHUB_RUN_NUMBER")
	runURL := flagStringOrEnv(cmd, "run-url", "")
	repoFull := flagStringOrEnv(cmd, "repo", "GITHUB_REPOSITORY")
	token := flagStringOrEnv(cmd, "token", "GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("REEVE_GITHUB_TOKEN")
	}
	actor := flagStringOrEnv(cmd, "actor", "GITHUB_ACTOR")
	if !local && (pr == 0 || repoFull == "" || token == "") {
		return fmt.Errorf("refresh requires --pr, --repo (or $GITHUB_REPOSITORY), and --token (or $GITHUB_TOKEN); use --local to refresh every declared stack")
	}

	env, err := loadRunEnv(cmd)
	if err != nil {
		return err
	}
	cfg, root, store, engine, authReg := env.cfg, env.root, env.store, env.engine, env.authReg

	lockStore := blocks.New(store)
	if n, _ := lockStore.ReapAll(ctx, run.LockTTL(cfg.Shared)); n > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "reaped %d expired lock(s)\n", n)
	}

	engineCfg := env.engineCfg

	in := run.RefreshInput{
		PRNumber:     pr,
		CommitSHA:    sha,
		RunNumber:    runNum,
		CIRunURL:     runURL,
		RepoRoot:     root,
		RepoFull:     repoFull,
		Actor:        actor,
		Engine:       engine,
		Config:       engineCfg,
		Shared:       cfg.Shared,
		AuthConfig:   cfg.Auth,
		AuthRegistry: authReg,
		Blob:         store,
		Locks:        lockStore,
		AuditWriter:  audit.NewWriter(store),
		DryRun:       flagBool(cmd, "dry-run"),
		All:          flagBool(cmd, "all"),
		Local:        local,
	}

	if !local {
		parts := strings.SplitN(repoFull, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid --repo %q: want owner/name", repoFull)
		}
		client, cerr := gh.New(ctx, token, parts[0], parts[1])
		if cerr != nil {
			return cerr
		}
		if prMeta, gerr := client.GetPR(ctx, pr); gerr == nil && prMeta.HeadSHA != "" {
			in.CommitSHA = prMeta.HeadSHA
		}
		in.VCS = client
	}

	out, err := run.Refresh(ctx, in)
	if err != nil {
		return err
	}
	if local || in.VCS == nil {
		fmt.Fprintln(cmd.OutOrStdout(), out.CommentBody)
	}
	if out.Failed {
		return fmt.Errorf("refresh failed for one or more stacks (run_id=%s)", out.RunID)
	}
	if out.Blocked {
		fmt.Fprintf(cmd.OutOrStdout(), "refresh blocked for one or more stacks (run_id=%s)\n", out.RunID)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "refresh complete (run_id=%s, %d stacks, %ds)\n",
		out.RunID, len(out.Stacks), out.DurationSec)
	return nil
}
