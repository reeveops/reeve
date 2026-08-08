# Tasks

- [x] `schemas.BindingYAML`: add `Local []string` (`local:`)
- [x] `auth.Binding`: add `Local []string`; add `ResolveWithOpts` +
      `ResolveOpts{Local, LocalProviders}`; second-pass scope replacement
- [x] `auth.IsCIOnlyType` helper
- [x] `auth.Validate`: `local:` refs declared + not CI-only
- [x] `factory.ValidateLint`: thread `Local` through bindings
- [x] `run.ResolveAuthEnv`: `LocalAuth` param, hint on local acquire failure
- [x] `run.PreviewInput.LocalAuthProviders`; thread from CLI
- [x] `cmd`: `--local-auth` flag on preview/plan-run/render; require `--local`
- [x] Tests: resolution substitution, CLI-wins ordering, passthrough,
      validate errors, lint
- [x] Docs: `docs/auth.md` local development section
- [x] Spec delta (this change)
