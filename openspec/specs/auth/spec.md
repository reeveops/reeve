# Auth

## Principle

Zero-trust. Short-lived federated credentials only. reeve consumes creds;
it does not configure them for the user. No long-lived secrets stored.

## Provider types (v1)

| Category | Types |
|---|---|
| Cloud federation | `aws_oidc`, `gcp_wif`, `azure_federated` |
| Identity | `github_app` |
| Secret managers | `aws_secrets_manager`, `aws_ssm_parameter`, `gcp_secret_manager`, `azure_key_vault`, `github_secret` |
| Vault | `vault`, `vault_dynamic_secret` |
| Local dev (CI-refused) | `aws_profile`, `aws_sso`, `gcloud_adc` |
| Escape hatch (flagged) | `env_passthrough` |

## Binding resolution

```yaml
bindings:
  - match: { stack: "prod/*" }
    providers: [aws-prod, gcp-prod]
  - match: { stack: "prod/*", mode: drift }
    providers: [aws-prod-readonly]
  - match: { stack: "prod/payments" }
    override: [aws-payments-strict]
    providers: [github-app]
```

Rules:

- Union providers across all matching bindings, dedup by name.
- `mode:` field matches only when that run mode is active (`preview`,
  `apply`, `drift`). No `mode:` applies to all modes.
- Each stack executes exactly once per run regardless of matches.
- Conflicting providers of the same logical scope error at lint time.
- `override:` explicitly replaces providers from more-general bindings.
- All credentials acquired before run, discarded after.
- `duration:` defaults to 1h; lint warns above 4h.

## Hardening

- Local providers **refuse** under `CI=true`. There is no CLI override.
- `env_passthrough` requires `providers.<name>.i_understand_this_is_dangerous: true`
  AND emits a loud warning every run. Lint flags as ERROR without the field.
- Fork PRs receive dry-run-only credentials by default. Full creds require
  explicit per-repo opt-in documented in the repo config.
- A credential exchange **fails** when the provider response omits the token
  or carries an unparseable expiry. `Credential.ExpiresAt` treats the zero
  value as "no expiry", so accepting a malformed timestamp would advertise a
  short-lived token as permanent - the wrong default direction for anything
  federated.

## `state.secrets_provider` boundary

Engine state secrets (Pulumi's passphrase/KMS for stack state) live in
engine config (§8.6), separate from runtime creds in `auth.yaml`. The boundary
is documented in user-facing docs to avoid confusion.
