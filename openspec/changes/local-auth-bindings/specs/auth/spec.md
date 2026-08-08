# Auth — deltas

## MODIFIED: Binding resolution

Bindings gain a `local:` provider list:

```yaml
bindings:
  - match: { stack: "prod/*" }
    providers: [gcp-prod]
    local: [gcp-local]
```

Rules (additions):

- `local:` is consulted **only** in `--local` runs (`run preview --local`,
  `plan-run`, `render`). CI runs ignore it.
- In a local run, each `local:` entry replaces every already-resolved
  provider of the same logical scope (same rule as `override:`), applied
  after normal resolution so it wins over any matched binding's
  `providers:`. Providers with no same-scope local substitute pass through
  unchanged.
- The `--local-auth <name>[,name...]` CLI flag applies the same scope
  replacement after config, so the flag wins. It requires `--local`.

## MODIFIED: Hardening

- `local:` entries must reference declared providers; lint errors
  otherwise.
- `local:` entries must not be CI-only federation types (`aws_oidc`,
  `gcp_wif`, `azure_federated`); lint errors — they can never acquire
  outside GitHub Actions, making the entry dead config.
- The `CI=true` refusal for `aws_profile` / `aws_sso` / `gcloud_adc` is
  unchanged and still has no CLI override.
