package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/blob/factory"
	blocks "github.com/reeveops/reeve/internal/blob/locks"
	"github.com/reeveops/reeve/internal/config"
	"github.com/reeveops/reeve/internal/run"
)

func newMaintenanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Run explicit bucket maintenance",
	}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Reap expired locks and prune expired run artifacts",
		RunE:  runMaintenance,
	}
	runCmd.Flags().String("root", "", "Repo root (default: cwd)")
	cmd.AddCommand(runCmd)
	return cmd
}

func runMaintenance(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	root, err := resolveRoot(cmd)
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
		return fmt.Errorf("open bucket: %w", err)
	}

	reaped, reapErr := blocks.New(store).ReapAll(ctx, run.LockTTL(cfg.Shared))
	pruned, retentionEnabled, pruneErr := run.PruneConfiguredRunArtifacts(ctx, store, cfg.Shared, time.Now())

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "expired locks reaped: %d\n", reaped)
	if retentionEnabled {
		fmt.Fprintf(w, "expired run artifacts pruned: %d\n", pruned)
	} else {
		fmt.Fprintln(w, "run artifact retention: disabled")
	}

	return errors.Join(
		wrapMaintenanceError("reap expired locks", reapErr),
		wrapMaintenanceError("prune run artifacts", pruneErr),
	)
}

func wrapMaintenanceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
