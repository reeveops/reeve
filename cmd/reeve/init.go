package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/config/scaffold"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/iac/hcl"
	"github.com/FynxLabs/reeve/internal/iac/pulumi"
)

// stdinIsTTY reports whether stdin is an interactive terminal. Package var so
// tests can inject either answer without a real TTY.
var stdinIsTTY = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold .reeve/ configuration for this repository",
		Long: `Scaffold a .reeve/ configuration directory with sane defaults.

reeve init inspects the repository, discovers Pulumi stacks and Terraform
root modules (the same scan as ` + "`reeve stacks discover`" + `), and writes
strict-loader-clean YAML:

  .reeve/shared.yaml         bucket, approvals, preconditions, apply gates
  .reeve/<engine>.yaml       engine config + discovered stack declarations
                             (pulumi.yaml, terraform.yaml, or tofu.yaml)
  .reeve/notifications.yaml  only when a Slack channel is configured

Modes:

  interactive (default on a terminal): a short wizard walks through the
  optional gates - approvals (CODEOWNERS-based or an explicit approver list
  plus required count), a commented freeze-window example, a Slack
  notification channel, and an approval-freshness window. Gates you skip are
  written as commented best-practice examples, off by default.

  --non-interactive / -n (auto-selected when stdin is not a terminal, and
  the only mode in minimal builds): zero prompts. The engine is detected
  from repo files (Pulumi.yaml -> pulumi, root-module *.tf -> terraform;
  OpenTofu is never auto-picked - select it in the wizard or set
  engine.type: tofu), stacks are scanned and pre-filled, and a safe
  baseline is written with every optional gate off.

Idempotency: an existing .reeve/ is never clobbered - init fills in only
the missing config types and leaves existing files untouched. Use --force
to regenerate everything (originals are kept as *.bak).

After init: review the files, run ` + "`reeve lint`" + `, commit .reeve/, and add
the GitHub Actions workflow printed at the end of the run.`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
	cmd.Flags().BoolP("non-interactive", "n", false,
		"No prompts: detect the engine, scan stacks, write safe baseline defaults")
	cmd.Flags().Bool("force", false,
		"Overwrite existing .reeve/ config files (originals are kept as *.bak)")
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	root, _ := os.Getwd()
	w := cmd.OutOrStdout()
	force := flagBool(cmd, "force")
	nonInteractive := flagBool(cmd, "non-interactive")
	interactive := !nonInteractive && stdinIsTTY()

	dir := filepath.Join(root, ".reeve")
	existing, err := scaffold.ExistingTypes(dir)
	if err != nil {
		return err
	}

	// Stack scan: filesystem walks for Pulumi.yaml projects and terraform
	// root modules (no engine binaries needed), clustered into suggested
	// declarations - the same path as `reeve stacks discover --write`.
	pulumiEnum, scanErr := pulumi.New("").EnumerateStacks(cmd.Context(), root)
	if scanErr != nil {
		fmt.Fprintf(w, "warning: pulumi stack scan failed (%v); continuing with an empty stacks: block\n", scanErr)
	}
	tfEnum, tfScanErr := hcl.ScanStacks(root)
	if tfScanErr != nil {
		fmt.Fprintf(w, "warning: terraform root-module scan failed (%v); continuing with an empty stacks: block\n", tfScanErr)
	}
	declsByEngine := map[string][]discovery.Declaration{
		"pulumi":    discovery.Cluster(pulumiEnum),
		"terraform": discovery.Cluster(tfEnum),
		"tofu":      discovery.Cluster(tfEnum), // OpenTofu shares the terraform layout
	}
	printDiscoveredStacks(w, "Pulumi", pulumiEnum, declsByEngine["pulumi"])
	printDiscoveredStacks(w, "Terraform", tfEnum, declsByEngine["terraform"])

	var opts scaffold.Options
	if interactive {
		if len(existing) > 0 && !force {
			fmt.Fprintf(w, "Existing .reeve/ config found (%s): only missing config types will be written; use --force to regenerate.\n\n",
				strings.Join(existingSummary(existing), ", "))
		}
		opts, err = runInitWizard(suggestEngine(pulumiEnum, tfEnum), declsByEngine)
		if err != nil {
			return err
		}
	} else {
		engine := detectEngine(w, pulumiEnum, tfEnum)
		opts = scaffold.Options{EngineType: engine, Stacks: declsByEngine[engine]}
	}

	files, err := scaffold.Render(opts)
	if err != nil {
		return err
	}

	written, skipped, err := writeScaffold(dir, files, existing, force)
	if err != nil {
		return err
	}

	for _, s := range skipped {
		fmt.Fprintf(w, "kept    %s\n", s)
	}
	for _, name := range written {
		fmt.Fprintf(w, "wrote   %s\n", filepath.Join(".reeve", name))
	}
	if len(written) == 0 {
		fmt.Fprintln(w, "\nNothing to do - every config type already exists. Use --force to regenerate.")
		return nil
	}

	// Sanity: everything on disk (ours + pre-existing) must pass the strict
	// loader. A failure here can only come from pre-existing files.
	if cfg, err := config.Load(root); err != nil {
		fmt.Fprintf(w, "\nwarning: .reeve/ does not pass the strict loader: %v\n", err)
	} else if err := cfg.Validate(); err != nil {
		fmt.Fprintf(w, "\nwarning: .reeve/ does not validate: %v\n", err)
	}

	printNextSteps(w)
	return nil
}

// detectEngine picks the engine for non-interactive mode from repo files:
// Pulumi.yaml projects -> pulumi, terraform root modules -> terraform.
// OpenTofu is never auto-picked: the tofu CLI consumes the same *.tf files,
// so choosing it is always an explicit user decision (wizard selection or
// engine.type: tofu).
func detectEngine(w io.Writer, pulumiEnum, tfEnum []discovery.Stack) string {
	switch {
	case len(pulumiEnum) > 0:
		return "pulumi"
	case len(tfEnum) > 0:
		fmt.Fprintln(w, "note: terraform root modules detected - scaffolding terraform engine config (OpenTofu users: set engine.type: tofu, or pick it in the interactive wizard).")
		return "terraform"
	default:
		fmt.Fprintln(w, "note: no Pulumi projects or Terraform root modules found - writing an empty stacks: block; re-run `reeve stacks discover --write` once projects exist.")
		return "pulumi"
	}
}

// suggestEngine is detectEngine without the console notes - the wizard's
// pre-selected default.
func suggestEngine(pulumiEnum, tfEnum []discovery.Stack) string {
	if len(pulumiEnum) == 0 && len(tfEnum) > 0 {
		return "terraform"
	}
	return "pulumi"
}

// writeScaffold writes the rendered files into dir, skipping any whose
// config_type already exists (unless force). With force, overwritten files
// are first copied to *.bak.
func writeScaffold(dir string, files []scaffold.File, existing map[string]string, force bool) (written, skipped []string, err error) {
	var root *os.Root
	defer func() {
		if root != nil {
			_ = root.Close()
		}
	}()
	for _, f := range files {
		if prev, ok := existing[f.ConfigType]; ok && !force {
			skipped = append(skipped, fmt.Sprintf("%s (config_type %s already declared there)", filepath.Join(".reeve", prev), f.ConfigType))
			continue
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, nil, err
		}
		// Opened lazily, on the first file actually written, so a run where
		// every config_type is skipped still leaves no .reeve/ behind.
		// Scoped so a symlink planted at .reeve/<name> cannot redirect the
		// write (or its .bak) outside the directory.
		if root == nil {
			r, err := os.OpenRoot(dir)
			if err != nil {
				return nil, nil, err
			}
			root = r
		}
		path := filepath.Join(dir, f.Name) // for messages only
		// With --force, an existing file of the same config_type may live
		// under a different name; back up and replace the same-named file,
		// which is the conventional location.
		if old, readErr := root.ReadFile(f.Name); readErr == nil {
			if err := root.WriteFile(f.Name+".bak", old, 0o600); err != nil {
				return nil, nil, fmt.Errorf("backup %s: %w", path, err)
			}
		}
		if err := root.WriteFile(f.Name, f.Content, 0o600); err != nil {
			return nil, nil, err
		}
		written = append(written, f.Name)
	}
	return written, skipped, nil
}

func existingSummary(existing map[string]string) []string {
	out := make([]string, 0, len(existing))
	for t, f := range existing {
		out = append(out, fmt.Sprintf("%s in %s", t, f))
	}
	// Deterministic order for output and tests.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func printDiscoveredStacks(w io.Writer, label string, enum []discovery.Stack, decls []discovery.Declaration) {
	if len(enum) == 0 {
		return
	}
	fmt.Fprintf(w, "Discovered %d %s stack(s) -> %d stack config entr%s:\n", len(enum), label, len(decls), plural(len(decls), "y", "ies"))
	for _, d := range decls {
		if d.Pattern != "" {
			fmt.Fprintf(w, "  pattern=%q stacks=%v\n", d.Pattern, d.Stacks)
		} else {
			fmt.Fprintf(w, "  project=%s path=%s stacks=%v\n", d.Project, d.Path, d.Stacks)
		}
	}
	fmt.Fprintln(w)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func printNextSteps(w io.Writer) {
	fmt.Fprint(w, `
Next steps:
  1. Review the generated files under .reeve/ (settings you skipped are
     included as comments), then commit the directory.
  2. Validate:            reeve lint
  3. Inspect stacks:      reeve stacks
  4. Dry-run the comment: reeve plan-run --sha $(git rev-parse HEAD) --run-number 1
  5. Add the GitHub Actions workflow (.github/workflows/reeve.yml):

       name: reeve
       on:
         pull_request:
           types: [opened, synchronize, reopened, ready_for_review]
         issue_comment:
           types: [created]
       permissions:
         contents: read
         pull-requests: write
         issues: write
         id-token: write
       concurrency:
         group: reeve-${{ github.event.pull_request.number || github.event.issue.number }}
         cancel-in-progress: false
       jobs:
         reeve:
           runs-on: ubuntu-latest
           steps:
             - uses: actions/checkout@v4
             - uses: FynxLabs/reeve@master
               with:
                 pulumi-version: latest

See docs/getting-started.md for the full walk-through.
`)
}
