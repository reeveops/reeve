package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/config"
)

func newMigrateConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate-config",
		Short: "Migrate .reeve/*.yaml files to the latest schema version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, _ := os.Getwd()
			dir := filepath.Join(root, ".reeve")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			m := config.NewMigrator()
			if err := m.MigrateDirectory(dir, dryRun); err != nil {
				return err
			}
			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "dry-run complete")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "migration complete (original files backed up as *.bak)")
			}
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "Show what would change without writing")
	return cmd
}
