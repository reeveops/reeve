package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"

	authfac "github.com/reeveops/reeve/internal/auth/factory"
	"github.com/reeveops/reeve/internal/config"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/vcs/codeowners"
)

func newLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Static check across all .reeve/*.yaml files",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := os.Getwd()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			// ${env:...} references outside the designated allow-list are
			// kept literal; surface them so typos and unsupported
			// placements don't fail silently at run time.
			for _, w := range cfg.EnvExpandWarnings {
				fmt.Fprintf(os.Stderr, "⚠️  %s\n", w)
			}
			// Engine configs: every engine.type must resolve to a compiled-in
			// engine, so a typo'd (or not-yet-shipped) type fails the CI gate
			// here instead of at run time.
			engines := make([]iac.Engine, len(cfg.Engines))
			for i, ec := range cfg.Engines {
				e, err := iac.New(ec.Engine)
				if err != nil {
					return err
				}
				engines[i] = e
			}
			// Freeze windows: reject unparseable cron or duration here so a
			// typo fails the CI gate instead of silently disabling the freeze.
			for _, w := range cfg.Shared.FreezeWindows {
				if _, err := cron.ParseStandard(w.Cron); err != nil {
					return fmt.Errorf("freeze window %q: invalid cron %q: %w", w.Name, w.Cron, err)
				}
				if w.Duration != "" {
					if _, err := time.ParseDuration(w.Duration); err != nil {
						return fmt.Errorf("freeze window %q: invalid duration %q (Go duration, e.g. 48h not 2d): %w", w.Name, w.Duration, err)
					}
				}
			}
			// Drift-channel event names and empty-on subscriptions are validated
			// by cfg.Validate() above (validateChannels), which is drift-event
			// strict for drift.yaml and exempts channels with default
			// subscriptions - one source of truth, so lint and runtime agree.
			// Preconditions: require_checks_passing and require_up_to_date
			// are *bool and default to DISABLED when omitted, which is
			// deliberate (reeve favours an easy first run). But a repo that
			// has wired up an apply flow and never set them will apply with
			// red CI, and the omission is invisible. Say so once.
			lintPreconditionGates(cfg.Shared)
			// CODEOWNERS: email owners cannot be matched to VCS logins, so
			// reeve's codeowners gate ignores them. Flag them here so a
			// path owned only by emails isn't silently unenforced.
			lintCodeownersEmails(root)
			// Auth lint: surfaces conflicts and dangerous providers.
			if cfg.Auth != nil {
				// Collect declared stack refs for the conflict check.
				var stacks []string
				engineCfg := cfg.Engines[0]
				engine := engines[0]
				enum, err := engine.EnumerateStacks(cmd.Context(), root)
				if err != nil {
					return fmt.Errorf("enumerate stacks (is %s installed and the project valid?): %w", engine.Name(), err)
				}
				decls := make([]discovery.Declaration, 0, len(engineCfg.Engine.Stacks))
				for _, s := range engineCfg.Engine.Stacks {
					decls = append(decls, discovery.Declaration{
						Project: s.Project, Path: s.Path, Pattern: s.Pattern, Stacks: s.Stacks,
					})
				}
				resolved := discovery.Resolve(enum, decls, discovery.Filter{})
				for _, s := range resolved {
					stacks = append(stacks, s.Ref())
				}
				if err := authfac.ValidateLint(cfg.Auth, stacks); err != nil {
					return fmt.Errorf("auth lint: %w", err)
				}
			}
			authN := 0
			if cfg.Auth != nil {
				authN = len(cfg.Auth.Providers)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK - %d engine config(s) loaded, bucket=%s, auth_providers=%d\n",
				len(cfg.Engines), cfg.Shared.Bucket.Type, authN)
			return nil
		},
	}
	return cmd
}

// lintCodeownersEmails warns about email owners in the repo's CODEOWNERS
// file. GitHub accepts them, but reeve has no commit-email → login
// resolution, so the approvals gate cannot enforce them (they are ignored
// at evaluation time). Same candidate paths as the VCS adapter's
// FetchCodeowners.
func lintCodeownersEmails(root string) {
	// Scoped to the repo root: CODEOWNERS is read from the PR HEAD, where it
	// could be a symlink out of the tree.
	repo, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer repo.Close()
	for _, rel := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		f, err := repo.Open(rel)
		if err != nil {
			continue
		}
		rules := codeowners.Parse(f)
		_ = f.Close()
		for _, r := range rules {
			for _, o := range r.Owners {
				if strings.Contains(strings.TrimPrefix(o, "@"), "@") {
					fmt.Fprintf(os.Stderr, "⚠️  %s: owner %q (pattern %q) is an email address - reeve cannot match emails to logins, so this owner is unenforceable\n", rel, o, r.Pattern)
				}
			}
		}
		return // first candidate found wins, matching FetchCodeowners
	}
}

// lintPreconditionGates warns when a gate that defaults to disabled was
// never given an explicit value. Only an *omitted* setting warns - an
// explicit `false` is an informed choice and stays silent, which is what
// the *bool in the schema is for.
func lintPreconditionGates(shared *schemas.Shared) {
	if shared == nil {
		return
	}
	if shared.Preconditions.RequireChecksPassing == nil {
		fmt.Fprintf(os.Stderr, "⚠️  preconditions.require_checks_passing is not set: "+
			"applies will proceed with failing CI checks. Set it explicitly to silence this.\n")
	}
	// require_up_to_date is intentionally off under merge-then-apply: after
	// the merge the base has advanced past the PR HEAD, so the gate can only
	// ever block. Only nag in the comment-triggered (apply-then-merge) flow.
	if shared.Apply.TriggerMode() == schemas.ApplyTriggerComment &&
		shared.Preconditions.RequireUpToDate == nil {
		fmt.Fprintf(os.Stderr, "⚠️  preconditions.require_up_to_date is not set: "+
			"applies will proceed from a branch behind base. Set it explicitly to silence this.\n")
	}
}
