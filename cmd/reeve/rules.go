package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/core/approvals"
	"github.com/FynxLabs/reeve/internal/run"
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rules", Short: "Inspect approval / precondition rules"}
	cmd.AddCommand(&cobra.Command{
		Use:   "explain <project/stack>",
		Short: "Show approval rule resolution trace for a stack",
		Args:  cobra.ExactArgs(1),
		RunE:  rulesExplain,
	})
	return cmd
}

func rulesExplain(cmd *cobra.Command, args []string) error {
	root, _ := os.Getwd()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ref := args[0]
	appCfg := run.ApprovalsConfigFor(cfg.Shared)
	resolved := approvals.Resolve(appCfg, ref)

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "rules for %s:\n", ref)
	fmt.Fprintf(w, "  required_approvals:   %d\n", resolved.RequiredApprovals)
	fmt.Fprintf(w, "  require_all_groups:   %v\n", resolved.RequireAllGroups)
	fmt.Fprintf(w, "  codeowners:           %v\n", resolved.Codeowners)
	fmt.Fprintf(w, "  dismiss_on_new_commit: %v\n", resolved.DismissOnNewCommit)
	if resolved.Freshness > 0 {
		fmt.Fprintf(w, "  freshness:            %s\n", resolved.Freshness)
	}
	fmt.Fprintf(w, "  approvers:\n")
	for _, a := range resolved.Approvers {
		fmt.Fprintf(w, "    - %s\n", a)
	}
	return nil
}
