package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/reeveops/reeve/internal/auth"
	authfac "github.com/reeveops/reeve/internal/auth/factory"
	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/factory"
	"github.com/reeveops/reeve/internal/config"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/iac"
	"github.com/reeveops/reeve/internal/observability/annotations"
	"github.com/reeveops/reeve/internal/run"
)

// runEnv is the wiring every repo-context command needs: a validated
// config, somewhere to put artifacts, an engine adapter and credentials.
//
// preview, apply, refresh and drift each built this by hand and had already
// drifted apart - apply dropped the filepath.Abs error the others checked.
// Collecting it here also confines the cfg.Engines[0] lookup for these
// commands to one place - the seam a multi-engine config widens. Commands
// that do not take this path (explain, lint, stacks) still index it
// themselves.
type runEnv struct {
	ctx    context.Context
	cfg    *config.Config
	root   string
	store  blob.Store
	engine iac.Engine
	// engineCfg is the config body behind engine. Held alongside it so no
	// command indexes cfg.Engines itself.
	engineCfg *schemas.Engine
	authReg   *auth.Registry
	// emitters is built before the store is opened. Annotation config is
	// the last thing validated, and a bad type must not first reap locks
	// and prune artifacts and only then fail - keeping the construction
	// here makes that ordering structural instead of something each
	// command has to remember.
	emitters []annotations.Emitter
}

// loadRunEnv resolves the repo root, loads and validates config, installs
// the logger, opens the blob store, constructs the engine adapter and
// builds the auth registry.
func loadRunEnv(cmd *cobra.Command) (*runEnv, error) {
	ctx := cmd.Context()

	root, err := resolveRoot(cmd)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	applyLogConfig(cfg.LogSettings())
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Comment rendering flags override the loaded config here, the one seam
	// every repo-context command passes through, so a flag cannot take effect
	// on one command and be silently ignored on another. Applied after Validate
	// so a flag is checked against its own allowed set rather than the
	// schema's.
	if err := applyCommentFlags(cmd, cfg.Shared); err != nil {
		return nil, err
	}

	emitters, err := run.BuildAnnotationEmitters(cfg.Observability)
	if err != nil {
		return nil, err
	}

	// Each construction step below can block on the network - opening the
	// bucket, and especially the federated credential exchange - so each
	// one reports its own duration. Without these the run shows a single
	// multi-minute gap after "config loaded" with nothing to attribute it
	// to.
	t := time.Now()
	store, err := factory.Open(ctx, cfg.Shared.Bucket, root)
	if err != nil {
		return nil, err
	}
	slog.Debug("blob store opened", "type", cfg.Shared.Bucket.Type, "ms", time.Since(t).Milliseconds())

	// The one place a repo-context command resolves which engine to use.
	// Single-engine today - config.Validate rejects multi-engine configs -
	// so this is the seam a multi-engine config would widen.
	engineCfg := cfg.Engines[0]
	t = time.Now()
	engine, err := iac.New(engineCfg.Engine)
	if err != nil {
		return nil, err
	}
	slog.Debug("engine adapter constructed", "engine", engine.Name(), "ms", time.Since(t).Milliseconds())

	t = time.Now()
	authReg, err := authfac.Build(ctx, cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("build auth registry: %w", err)
	}
	slog.Debug("auth registry built", "ms", time.Since(t).Milliseconds())

	return &runEnv{
		ctx: ctx, cfg: cfg, root: root, store: store,
		engine: engine, engineCfg: engineCfg, authReg: authReg, emitters: emitters,
	}, nil
}

// resolveRoot returns the absolute repo root: --root, else the working
// directory.
func resolveRoot(cmd *cobra.Command) (string, error) {
	root := flagStringOrDefault(cmd, "root", "")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve repo root: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repo root %q: %w", root, err)
	}
	return abs, nil
}

// repoPathForRoot returns root relative to the repository checkout used by
// GitHub Actions. Outside Actions, VCS paths retain their existing behavior.
func repoPathForRoot(root string) string {
	workspace := os.Getenv("GITHUB_WORKSPACE")
	if workspace == "" {
		return "."
	}
	rel, err := filepath.Rel(workspace, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "."
	}
	return filepath.ToSlash(rel)
}
