# Design

## Config shape

```yaml
# .reeve/auth.yaml
providers:
  gcp-prod:
    type: gcp_wif
    workload_identity_provider: projects/.../providers/reeve
    service_account: reeve@proj.iam.gserviceaccount.com
  gcp-local:
    type: gcloud_adc

bindings:
  - match: { stack: "prod/*" }
    providers: [gcp-prod]
    local: [gcp-local]        # NEW: substitutes in --local runs only
```

## Resolution semantics

`ResolveWithOpts(bindings, decls, ref, mode, opts)`:

1. Resolve exactly as today (sorted general → specific; `override:`
   replaces same-scope; `providers:` unions).
2. If `opts.Local`: walk the sorted matched bindings a second time; each
   name in a binding's `local:` list is applied via the existing
   `replaceScope` (removes every already-resolved provider of the same
   declared scope, appends the local one). Second pass ⇒ `local:` always
   wins over providers added by later, more-specific bindings.
3. Apply `opts.LocalProviders` (the `--local-auth` flag values) the same
   way, last ⇒ CLI wins over config.

Providers with no local substitute pass through unchanged — a stack bound
only to a secret-manager provider keeps it in local runs.

## Scope replacement, not removal

Replacing by scope (aws / gcp / azure / github-identity, secret managers
keyed per-type) is what makes one `local:` entry cleanly displace a WIF
provider without touching, say, a `gcp_secret_manager` provider bound to
the same stack. This is the same rule `override:` already uses.

## Threading `local` to resolution

`run.ResolveAuthEnv` gains a `LocalAuth{Enabled bool, Providers []string}`
parameter. `run.Preview` passes `{in.Local, in.LocalAuthProviders}`;
apply / refresh / drift pass the zero value (they are never local).

On acquisition failure with `Enabled: true`, the error is wrapped with a
hint naming `local:` and `--local-auth`.

## Lint (factory.ValidateLint + auth.Validate)

- `local:` entry referencing an undeclared provider → error.
- `local:` entry whose declared type is `aws_oidc` / `gcp_wif` /
  `azure_federated` → error (`IsCIOnlyType`): these acquire GitHub Actions
  OIDC tokens and can never succeed in the only mode where `local:` is
  consulted.
- Existing same-scope conflict validation is unchanged; `local:` entries
  are exempt from it because substitution (not union) guarantees at most
  one provider per scope.

## CLI

`--local-auth` is a `StringSlice` flag on preview/plan-run/render.
Setting it without `--local` is an error (on `plan-run`/`render` the alias
forces `--local`, so it always works there). Names are validated against
the registry at resolution time (unknown name → existing "provider %q not
registered" error).

## Interaction with the CI refusal

Local provider types still hard-refuse under `CI=true`. Consequence: a
`plan-run` inside GitHub Actions with `local:` bindings will substitute
and then refuse. That is intended — "local" means local; CI simulation
should use the CI providers by not declaring `local:` or by running
without the substitution taking effect (unset bindings). No override is
added.
