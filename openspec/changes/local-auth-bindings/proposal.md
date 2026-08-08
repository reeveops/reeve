# local-auth-bindings

## Why

`reeve run preview --local` (and its aliases `plan-run` / `render`) resolves
auth bindings exactly as CI does. If a stack is bound to `gcp_wif` /
`aws_oidc` / `azure_federated`, a local run tries to acquire a GitHub
Actions OIDC token and fails with `ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not
set`. The local-dev provider types (`aws_profile`, `aws_sso`, `gcloud_adc`)
exist, but there is no way to bind them "only when local":

- Binding both `gcp-wif` and `gcp-local` to the same stack is a lint error
  (same-scope conflict) and would double-acquire anyway.
- The `mode:` match field (`preview` | `apply` | `drift`) is orthogonal to
  local-vs-CI.

So today the only way to run locally against a WIF-bound stack is to edit
`auth.yaml` temporarily. That's the gap.

## What

1. **`local:` list on bindings** (config-first). In a `--local` run, each
   provider named in a matched binding's `local:` list replaces the
   already-resolved providers of the same credential scope (reusing the
   `override:` scope-replacement machinery). CI runs ignore the field
   entirely.

2. **`--local-auth <name>[,name...]` flag** on `run preview` / `plan-run` /
   `render` (CLI escape hatch). Each named declared provider replaces
   same-scope resolved providers for every stack in the run. Requires
   `--local`.

3. **Lint**: `local:` entries must reference declared providers and must not
   be CI-only federation types (`aws_oidc`, `gcp_wif`, `azure_federated`) —
   those cannot work locally, so naming one there is dead config.

4. **Better errors**: credential acquisition failures in `--local` runs get
   a hint pointing at `local:` binding lists and `--local-auth`.

## Scope

- In: binding schema + resolution, lint, preview/plan-run/render CLI, docs.
- Out: `run apply` (never local), drift (CI-only), any CLI override of the
  local-provider CI=true refusal (explicitly forbidden by the auth spec).

## Non-goals

- No new provider types. `gcloud_adc` (GCP application-default
  credentials), `aws_profile`, and `aws_sso` already cover local identity.
- No relaxation of the `CI=true` hard refusal for local provider types.
