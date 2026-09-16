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
- Credentials are acquired when first needed and discarded after the command.
- `duration:` defaults to 1h; lint warns above 4h.

## Invocation reuse

Preview and apply MUST reuse a credential by provider name within one command
invocation. Provider configuration fixes account, role, audience, and scope.

Concurrent requests for one provider MUST collapse into one acquisition. Failed
acquisitions are not cached, and later requests may retry.

Credentials expiring within 30 seconds MUST NOT be returned to a new consumer.
Every acquired generation remains owned until command cleanup runs exactly once.

#### Scenario: Stacks share one federation exchange

- **GIVEN** state auth and multiple preview stacks resolve the same provider
- **WHEN** they request credentials during one preview command
- **THEN** the provider is acquired once
- **AND** each consumer receives an independent environment map
- **AND** the provider cleanup runs once after every preview finishes

#### Scenario: Apply stacks share one federation exchange

- **GIVEN** state auth and multiple apply stacks resolve the same provider
- **WHEN** every independent gate passes and apply resolves credentials
- **THEN** the provider is acquired once during the command
- **AND** the provider cleanup runs once after every apply finishes

#### Scenario: A near-expiry generation is replaced

- **GIVEN** a cached credential expires within the safety margin
- **WHEN** another consumer requests the provider
- **THEN** the cache acquires a new generation
- **AND** both generations remain owned until command cleanup

## Hardening

- Local providers **refuse** under `CI=true`. There is no CLI override.
- `env_passthrough` requires `providers.<name>.i_understand_this_is_dangerous: true`
  AND emits a loud warning every run. Lint flags as ERROR without the field.
- Fork PRs receive dry-run-only credentials by default. Full creds require
  explicit per-repo opt-in documented in the repo config.
- A credential exchange **fails** when the provider response omits the
  token, omits or malforms the expiry, or reports an expiry that is not in
  the future. Applies to every provider that performs an exchange:
  `aws_oidc`, `gcp_wif`, `azure_federated`, `github_app`.
- `Credential.ExpiresAt` treats the zero value as "no expiry", so accepting
  a malformed or absent timestamp would advertise a short-lived token as
  permanent - the wrong default direction for anything federated.
- An expiry expressed as a relative `expires_in` MUST be bounded before
  conversion: a value large enough to overflow the duration type wraps and
  yields an expiry in the past.

## `state.secrets_provider` boundary

Engine state secrets (Pulumi's passphrase/KMS for stack state) live in
engine config (§8.6), separate from runtime creds in `auth.yaml`. The boundary
is documented in user-facing docs to avoid confusion.
