package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/audit"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/core/breakglass"
	"github.com/reeveops/reeve/internal/run"
	"github.com/reeveops/reeve/internal/vcs"
	gh "github.com/reeveops/reeve/internal/vcs/github"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Run apply for approved stacks",
		Long: `Run apply for the stacks affected by the PR, after evaluating locks,
preconditions, approvals, and freeze windows.

Exit codes:
  0  every targeted stack applied cleanly or was a no-op, or every stack was
     blocked by preconditions/locks. Blocked is a deliberate non-failure:
     gates held the apply back and nothing was attempted, so the check stays
     green and a later re-run can proceed.
  1  one or more stacks FAILED to apply (engine, auth, or lock-storage
     error), the run was cancelled by a signal, post-apply persistence
     failed, or the run itself errored before applying (config, VCS,
     storage). A failed apply is never a green check.`,
		RunE: runApply,
	}
	addPreviewFlags(cmd)
	cmd.Flags().String("trigger-source", "", "How this apply was initiated: comment (default) | merge. Compared against apply.trigger config; a mismatch is a no-op.")
	cmd.Flags().String("actor", "", "User triggering the apply (default: $GITHUB_ACTOR)")
	cmd.Flags().Bool("break-glass", false, "Emergency apply: override approvals (and freeze unless disabled); requires break_glass config and a justification")
	cmd.Flags().String("justification", "", "Mandatory justification for --break-glass (or parsed from $REEVE_BREAK_GLASS_COMMENT)")
	cmd.Flags().Bool("refresh", false,
		"Reconcile state with live infrastructure before applying. Turns plan locking OFF for this run: the applied change set is computed after the refresh, so it is not the plan the PR previewed.")
	return cmd
}

func runApply(cmd *cobra.Command, _ []string) error {
	// Cancelled by SIGINT/SIGTERM via the root signal context (see main.go).
	ctx := cmd.Context()

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
	ciRunID, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	selfNames := selfCheckNames()
	if pr == 0 || repoFull == "" || token == "" {
		return fmt.Errorf("apply requires --pr, --repo (or $GITHUB_REPOSITORY), and --token (or $GITHUB_TOKEN)")
	}

	env, err := loadRunEnv(cmd)
	if err != nil {
		return err
	}
	cfg, root, store, engine, authReg := env.cfg, env.root, env.store, env.engine, env.authReg
	annotationEmitters := env.emitters

	// Opportunistic reaper before acquiring any locks.
	lockStore := blocks.New(store)
	if n, _ := lockStore.ReapAll(ctx, run.LockTTL(cfg.Shared)); n > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "reaped %d expired lock(s)\n", n)
	}

	// Opportunistic blob retention: prune run artifacts older than max_age.
	run.PruneRunArtifactsOpportunistic(ctx, store, cfg.Shared)

	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid --repo %q (want owner/name)", repoFull)
	}
	client, err := gh.New(ctx, token, parts[0], parts[1])
	if err != nil {
		return err
	}

	if pr > 0 {
		if prMeta, err := client.GetPR(ctx, pr); err == nil && prMeta.HeadSHA != "" {
			sha = prMeta.HeadSHA
		}
	}

	// Break-glass: resolve the justification, either from --justification or
	// by strictly parsing the triggering comment ($REEVE_BREAK_GLASS_COMMENT,
	// set by action.yml). A malformed command posts a helpful PR comment and
	// runs NOTHING.
	force := flagBool(cmd, "force")
	var bgReq *run.BreakGlassRequest
	if flagBool(cmd, "break-glass") {
		justification := cmd.Flag("justification").Value.String()
		if strings.TrimSpace(justification) == "" {
			raw := os.Getenv("REEVE_BREAK_GLASS_COMMENT")
			if strings.TrimSpace(raw) == "" {
				return fmt.Errorf("--break-glass requires --justification %q (or $REEVE_BREAK_GLASS_COMMENT to parse)", "<non-empty text>")
			}
			parsed, perr := breakglass.ParseCommand(raw)
			if perr != nil {
				if cerr := client.PostComment(ctx, pr, breakglass.MalformedComment(perr)); cerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "posting malformed-command comment failed: %v\n", cerr)
				}
				return fmt.Errorf("break-glass command malformed (no run): %w", perr)
			}
			justification = parsed.Justification
			force = force || parsed.Force
		}
		bgReq = &run.BreakGlassRequest{Justification: justification}
	}

	engineCfg := env.engineCfg

	otelProvider, _ := run.BuildOTEL(ctx, cfg.Observability)
	defer func() {
		if otelProvider != nil {
			_ = otelProvider.Shutdown(ctx)
		}
	}()
	out, err := run.Apply(ctx, run.ApplyInput{
		PRNumber:        pr,
		TriggerSource:   flagStringOrDefault(cmd, "trigger-source", ""),
		CommitSHA:       sha,
		RunNumber:       runNum,
		CIRunID:         ciRunID,
		CIRunURL:        runURL,
		SelfCheckNames:  selfNames,
		RepoRoot:        root,
		RepoFull:        repoFull,
		Actor:           actor,
		Engine:          engine,
		Config:          engineCfg,
		Shared:          cfg.Shared,
		AuthConfig:      cfg.Auth,
		AuthRegistry:    authReg,
		Notifications:   cfg.Notifications,
		OTEL:            otelProvider,
		Annotations:     annotationEmitters,
		Blob:            store,
		Locks:           lockStore,
		VCS:             client,
		AuditWriter:     audit.NewWriter(store),
		Force:           force,
		Refresh:         flagBool(cmd, "refresh"),
		BreakGlass:      bgReq,
		CommentApproval: commentApprovalConfig(),
	})
	if err != nil {
		return err
	}

	// Exit-code contract: failed stacks always exit nonzero (a failed apply
	// must never render as a green check); blocked-only exits zero
	// (preconditions held the run back - nothing was attempted, nothing broke).
	if out.Failed {
		return applyFailedError(out, pr)
	}
	if out.Blocked {
		fmt.Fprintf(cmd.OutOrStdout(), "apply blocked by preconditions for one or more stacks (PR #%d, run_id=%s)\n", pr, out.RunID)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "apply complete (PR #%d, run_id=%s, %d stacks, %ds)\n",
		pr, out.RunID, len(out.Stacks), out.DurationSec)
	return nil
}

// commentApprovalConfig builds the pr_comment source config from the action
// inputs, threaded in as env vars. The defaults match action.yml's
// allowed-associations and command-prefix defaults, so an out-of-date action
// still fails closed to the restrictive allowlist rather than accepting any
// commenter. Only consulted when approvals.sources enables pr_comment.
func commentApprovalConfig() vcs.CommentApprovalConfig {
	return vcs.CommentApprovalConfig{
		CommandPrefixes:     splitCSV(envOrDefault("REEVE_COMMAND_PREFIXES", "/reeve")),
		AllowedAssociations: splitCSV(envOrDefault("REEVE_ALLOWED_ASSOCIATIONS", "OWNER,MEMBER,COLLABORATOR")),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyFailedError builds the nonzero-exit error naming every failed stack.
func applyFailedError(out *run.ApplyOutput, pr int) error {
	return fmt.Errorf("apply failed for %d stack(s): %s (PR #%d, run_id=%s)",
		len(out.FailedStacks), strings.Join(out.FailedStacks, ", "), pr, out.RunID)
}
