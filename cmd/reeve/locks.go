package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/blob/factory"
	blocks "github.com/FynxLabs/reeve/internal/blob/locks"
	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/run"
)

func newLocksCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "locks", Short: "Inspect or manage stack locks"}

	list := &cobra.Command{
		Use:   "list",
		Short: "Read-only bucket lock inspection",
		RunE:  locksList,
	}

	explain := &cobra.Command{
		Use:   "explain <project/stack>",
		Short: "Show lock holder, queue, TTL",
		Args:  cobra.ExactArgs(1),
		RunE:  locksExplain,
	}

	reap := &cobra.Command{
		Use:   "reap",
		Short: "Evict expired locks across the bucket (opportunistic - also runs on every invocation)",
		RunE:  locksReap,
	}

	unlock := &cobra.Command{
		Use:   "unlock [project/stack]",
		Short: "Admin-override unlock: clear a stack's holder and promote its queue; --pr N instead removes that PR from its locks (all of them when the stack is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  locksUnlock,
	}
	unlock.Flags().String("reason", "", "Required reason for the override (surfaces in logs)")
	unlock.Flags().String("actor", "", "User performing the override (default: $GITHUB_ACTOR or $USER)")
	unlock.Flags().Int("pr", 0, "Remove this PR from the lock's holder/queue instead of force-clearing the holder (closed or abandoned PR cleanup)")
	unlock.Flags().Bool("force", false, "With --pr: clear the PR's holder even if its lease is still active (likely mid-apply)")

	cmd.AddCommand(list, explain, reap, unlock)
	return cmd
}

// openLocks opens the lock store and returns the loaded config alongside
// it so callers can thread config-derived settings (locking.ttl, admin
// override) without re-loading.
func openLocks(cmd *cobra.Command) (*blocks.Store, *config.Config, error) {
	root, _ := os.Getwd()
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	store, err := factory.Open(cmd.Context(), cfg.Shared.Bucket, root)
	if err != nil {
		return nil, nil, fmt.Errorf("open bucket: %w", err)
	}
	return blocks.New(store), cfg, nil
}

func locksList(cmd *cobra.Command, _ []string) error {
	s, _, err := openLocks(cmd)
	if err != nil {
		return err
	}
	locks, err := s.ListAll(cmd.Context())
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if len(locks) == 0 {
		fmt.Fprintln(w, "no locks")
		return nil
	}
	now := time.Now()
	for _, l := range locks {
		st := l.Status(now)
		holder := "-"
		if l.Holder != nil {
			holder = fmt.Sprintf("PR #%d (expires %s)", l.Holder.PR, l.Holder.ExpiresAt)
		}
		fmt.Fprintf(w, "%s/%s  %s  holder=%s  queue=%d\n",
			l.Project, l.Stack, st, holder, len(l.Queue))
	}
	return nil
}

func locksExplain(cmd *cobra.Command, args []string) error {
	ref := args[0]
	parts := splitRef(ref)
	if parts == nil {
		return fmt.Errorf("expected project/stack, got %q", ref)
	}
	s, _, err := openLocks(cmd)
	if err != nil {
		return err
	}
	l, etag, err := s.Get(cmd.Context(), parts[0], parts[1])
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "lock: %s/%s\n", l.Project, l.Stack)
	fmt.Fprintf(w, "status: %s\n", l.Status(time.Now()))
	fmt.Fprintf(w, "etag: %s\n", shortEtag(etag))
	if l.Holder != nil {
		h := l.Holder
		fmt.Fprintf(w, "holder: PR #%d  actor=%s  run=%s  acquired=%s  expires=%s\n",
			h.PR, h.Actor, h.RunID, h.AcquiredAt, h.ExpiresAt)
	} else {
		fmt.Fprintln(w, "holder: (free)")
	}
	if len(l.Queue) > 0 {
		fmt.Fprintln(w, "queue:")
		for i, q := range l.Queue {
			fmt.Fprintf(w, "  %d. PR #%d  actor=%s  enqueued=%s\n", i+1, q.PR, q.Actor, q.EnqueuedAt)
		}
	}
	return nil
}

func locksReap(cmd *cobra.Command, _ []string) error {
	s, cfg, err := openLocks(cmd)
	if err != nil {
		return err
	}
	n, err := s.ReapAll(cmd.Context(), run.LockTTL(cfg.Shared))
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "reaped %d expired lock(s)\n", n)
	return nil
}

// locksUnlock is lock cleanup. Without --pr it is the admin override:
// force-clear the holder of one lock (or every lock, when the stack is
// omitted) and promote the queue - gated by locking.admin_override.
// With --pr N it removes that PR from the holder/queue instead - the
// escape hatch for PRs closed or merged while still holding or queued,
// without which an abandoned PR could be promoted to holder and block
// everyone until TTL. The --pr path is NOT admin-gated: it only ever
// touches that PR's own entries (parity with what a finishing apply
// does automatically), and it is what "/reeve unlock" PR comments
// dispatch - those are already gated by allowed-associations upstream.
func locksUnlock(cmd *cobra.Command, args []string) error {
	pr := flagInt(cmd, "pr")

	s, cfg, err := openLocks(cmd)
	if err != nil {
		return err
	}

	reason := flagStringOrDefault(cmd, "reason", "")
	actor := flagStringOrEnv(cmd, "actor", "GITHUB_ACTOR")
	if actor == "" {
		actor = os.Getenv("USER")
	}

	if pr <= 0 {
		// Admin gate: shared.yaml locking.admin_override. Force-clearing
		// other PRs' holders is the dangerous path; PR-scoped removal
		// below is not.
		admin := cfg.Shared.Locking.AdminOverride
		if admin.RequiresReason && reason == "" {
			return fmt.Errorf("locks unlock: --reason is required (shared.yaml locking.admin_override.requires_reason=true)")
		}
		if len(admin.Allowed) > 0 {
			if !actorAllowed(actor, admin.Allowed) {
				return fmt.Errorf("locks unlock: actor %q is not in locking.admin_override.allowed", actor)
			}
		}
	}

	ctx := cmd.Context()
	ttl := run.LockTTL(cfg.Shared)
	w := cmd.OutOrStdout()

	if pr <= 0 {
		// Force-unlock one stack's holder. There is deliberately no
		// bucket-wide force-unlock: "unlock everything" only exists
		// PR-scoped (--pr / "/reeve unlock"), never for the whole bucket.
		if len(args) == 0 {
			return fmt.Errorf("locks unlock: <project/stack> is required (or pass --pr N to remove a PR from its locks)")
		}
		parts := splitRef(args[0])
		if parts == nil {
			return fmt.Errorf("expected project/stack, got %q", args[0])
		}
		l, err := s.ForceUnlock(ctx, parts[0], parts[1], ttl)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "unlocked %s/%s by actor=%s reason=%q\n",
			parts[0], parts[1], actor, reason)
		if l.Holder != nil {
			fmt.Fprintf(w, "promoted PR #%d from queue\n", l.Holder.PR)
		}
		return nil
	}

	// --pr path: remove the PR from its own holder/queue entries, one
	// stack or all of them. Without --force, a holder whose lease is
	// still active (likely a live apply) is refused with a hint.
	force := flagBool(cmd, "force")
	if len(args) == 0 {
		n, active, err := s.UnlockPRAll(ctx, pr, "", ttl, force)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "removed PR #%d from %d lock(s) by actor=%s reason=%q\n", pr, n, actor, reason)
		if len(active) > 0 {
			return fmt.Errorf("PR #%d is in the middle of an apply on %s; if you are sure you want to unlock, re-run with --force", pr, strings.Join(active, ", "))
		}
		return nil
	}
	parts := splitRef(args[0])
	if parts == nil {
		return fmt.Errorf("expected project/stack, got %q", args[0])
	}
	l, err := s.UnlockPR(ctx, parts[0], parts[1], pr, "", ttl, force)
	if err != nil {
		if errors.Is(err, blocks.ErrHolderActive) {
			return fmt.Errorf("PR #%d is in the middle of an apply on %s/%s; if you are sure you want to unlock, re-run with --force", pr, parts[0], parts[1])
		}
		return err
	}
	fmt.Fprintf(w, "removed PR #%d from %s/%s by actor=%s reason=%q\n", pr, parts[0], parts[1], actor, reason)
	if l.Holder != nil {
		fmt.Fprintf(w, "holder is now PR #%d (expires %s)\n", l.Holder.PR, l.Holder.ExpiresAt)
	}
	return nil
}

func actorAllowed(actor string, allowed []string) bool {
	// allowed entries may be user logins (@alice) or team slugs (@org/team).
	// Phase 2 approximation: literal equality, with/without leading @. Team
	// expansion lands when the VCS adapter is wired here (Phase 4+).
	trimmed := actor
	if len(trimmed) > 0 && trimmed[0] == '@' {
		trimmed = trimmed[1:]
	}
	for _, a := range allowed {
		candidate := a
		if len(candidate) > 0 && candidate[0] == '@' {
			candidate = candidate[1:]
		}
		if candidate == trimmed {
			return true
		}
	}
	return false
}

func splitRef(ref string) []string {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '/' {
			return []string{ref[:i], ref[i+1:]}
		}
	}
	return nil
}

func shortEtag(e string) string {
	if len(e) > 12 {
		return e[:12] + "…"
	}
	return e
}
