package run

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/config/schemas"
)

// CleanupFunc runs all on-disk cleanups registered by credential providers.
// It is safe to call on a nil receiver and safe to call more than once.
type CleanupFunc func()

// LocalAuth carries --local run auth adjustments into resolution. The zero
// value means a CI run: bindings' `local:` lists are ignored.
type LocalAuth struct {
	// Enabled activates bindings' `local:` substitution lists.
	Enabled bool
	// Providers is the --local-auth CLI override; each named provider
	// replaces same-scope resolved providers, after config substitution.
	Providers []string
}

// ResolveAuthEnv returns the merged env var map for a single stack + mode
// plus a cleanup func the caller defers. If cfg is nil or has no bindings,
// returns empty env (the engine relies on ambient creds / $GITHUB_TOKEN).
//
// The cleanup func runs every Credential.Cleanup the providers attached
// (e.g. removing the GCP WIF on-disk credential file). Cleanup errors are
// logged but never propagated - they happen at end-of-run so the work has
// already shipped.
func ResolveAuthEnv(ctx context.Context, cfg *schemas.Auth, registry *auth.Registry, stackRef string, mode auth.Mode, local LocalAuth) (map[string]string, CleanupFunc, error) {
	noop := func() {}
	if cfg == nil || registry == nil {
		return nil, noop, nil
	}
	bindings := make([]auth.Binding, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		bindings = append(bindings, auth.Binding{
			StackPattern: b.Match.Stack,
			Mode:         auth.Mode(b.Match.Mode),
			Providers:    b.Providers,
			Override:     b.Override,
			Local:        b.Local,
		})
	}
	decls := make(map[string]auth.ProviderDecl, len(cfg.Providers))
	for n, p := range cfg.Providers {
		decls[n] = auth.ProviderDecl{Name: n, Type: p.Type}
	}
	names := auth.ResolveWithOpts(bindings, decls, stackRef, mode, auth.ResolveOpts{
		Local:          local.Enabled,
		LocalProviders: local.Providers,
	})
	if len(names) == 0 {
		return nil, noop, nil
	}
	env, creds, err := registry.AcquireAll(ctx, names)
	if err != nil {
		if local.Enabled {
			return nil, noop, fmt.Errorf("acquire creds for %s (%s): %w\nhint: in --local runs, bind local-safe providers (aws_profile/aws_sso/gcloud_adc) via a `local:` list on the binding in .reeve/auth.yaml, or pass --local-auth <provider>", stackRef, mode, err)
		}
		return nil, noop, fmt.Errorf("acquire creds for %s (%s): %w", stackRef, mode, err)
	}
	cleanup := func() {
		for _, c := range creds {
			if c == nil || c.Cleanup == nil {
				continue
			}
			if cerr := c.Cleanup(); cerr != nil {
				slog.Warn("credential cleanup failed",
					"provider", c.Source, "kind", c.Kind, "err", cerr)
			}
		}
	}
	return env, cleanup, nil
}
