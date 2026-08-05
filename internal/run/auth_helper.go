package run

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/FynxLabs/reeve/internal/auth"
	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// CleanupFunc runs all on-disk cleanups registered by credential providers.
// It is safe to call on a nil receiver and safe to call more than once.
type CleanupFunc func()

// ResolveAuthEnv returns the merged env var map for a single stack + mode
// plus a cleanup func the caller defers. If cfg is nil or has no bindings,
// returns empty env (the engine relies on ambient creds / $GITHUB_TOKEN).
//
// The cleanup func runs every Credential.Cleanup the providers attached
// (e.g. removing the GCP WIF on-disk credential file). Cleanup errors are
// logged but never propagated - they happen at end-of-run so the work has
// already shipped.
func ResolveAuthEnv(ctx context.Context, cfg *schemas.Auth, registry *auth.Registry, stackRef string, mode auth.Mode) (map[string]string, CleanupFunc, error) {
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
		})
	}
	decls := make(map[string]auth.ProviderDecl, len(cfg.Providers))
	for n, p := range cfg.Providers {
		decls[n] = auth.ProviderDecl{Name: n, Type: p.Type}
	}
	names := auth.ResolveWithDecls(bindings, decls, stackRef, mode)
	if len(names) == 0 {
		return nil, noop, nil
	}
	env, creds, err := registry.AcquireAll(ctx, names)
	if err != nil {
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
