# Authentication

Configure the identities Reeve needs for your environment, then bind workload credentials to stacks and run modes.
Federation is preferred; supported secret managers and explicitly acknowledged environment mappings cover other credentials.

## Which credentials go where

| Use | Where it is configured |
| --- | --- |
| GitHub controller API | Default Actions token, shared-workflow `reeve_token`, or action `github-token`. |
| Reeve bucket | Runner/cloud SDK credentials; [bucket authentication](github-actions.md#bucket-authentication). |
| Pulumi state backend | `engine.state.auth_provider` and `engine.state` settings. |
| Terraform/OpenTofu backend and cloud workload | Explicit credentials passed to the engine through bindings; backend settings stay in `.tf` files. |
| Pulumi workload | Stack/mode bindings in `.reeve/auth.yaml`. |

These identities may share a role where appropriate, but their configuration paths are distinct.
Adding a workload provider does not authenticate the controller bucket or change the identity posting PR comments.

Choose a provider below, complete its cloud-side setup, and validate the actual stack references with `reeve stacks` and `reeve lint`.
For ready-to-adapt wiring, see the [AWS](../examples/aws-oidc/README.md), [GCP](../examples/gcp-wif/README.md), and [multi-cloud](../examples/multi-cloud/README.md) recipes.

## Provider catalog

| Category | Types |
|---|---|
| Cloud federation | `aws_oidc`, `gcp_wif`, `azure_federated` |
| Identity | `github_app` |
| Secret managers | `aws_secrets_manager`, `aws_ssm_parameter`, `gcp_secret_manager`, `azure_key_vault`, `github_secret` |
| Local dev (CI-refused) | `aws_profile`, `aws_sso`, `gcloud_adc` |
| Escape hatch (flagged) | `env_passthrough` |

## Binding model

Providers are declared once under `providers:`. Bindings map stack
patterns (and optionally run modes) to sets of provider names:

```yaml
providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111:role/reeve-prod
  aws-prod-readonly:
    type: aws_oidc
    role_arn: arn:aws:iam::111:role/reeve-drift-readonly
  gcp-prod:
    type: gcp_wif
    workload_identity_provider: projects/111/locations/global/workloadIdentityPools/github/providers/reeve
    service_account: reeve-prod@prod.iam.gserviceaccount.com

bindings:
  # Default for preview + apply on prod stacks
  - match: { stack: "*/prod" }
    providers: [aws-prod, gcp-prod]

  # Drift-specific binding: read-only role
  - match: { stack: "*/prod", mode: drift }
    override: [aws-prod-readonly]

```

### Resolution rules

1. Bindings are walked general → specific.
2. A stack activates the **union** of providers across all matching
   bindings, deduplicated.
3. `match.mode: preview|apply|drift` narrows a binding to one run mode.
   No `mode:` means "all modes".
4. `override:` replaces entries from earlier, more general bindings with
   the same logical scope, derived from the providers' declared `type`
   (e.g. an `aws_oidc` override replaces every AWS-scoped provider
   regardless of naming).
5. Two providers of the same logical scope bound to one stack (e.g. two
   `aws_oidc` roles) is an error at lint time.
6. `local:` names providers that substitute for same-scope providers in
   `--local` runs only (see [Local development](#local-development)). CI
   runs ignore the field.

### Logical scopes

Used to detect conflicts:

- `aws` - `aws_oidc`, `aws_profile`, `aws_sso`
- `gcp` - `gcp_wif`, `gcloud_adc`
- `azure` - `azure_federated`
- `github-identity` - `github_app`
- Secret-manager scopes are distinct by provider type; different providers of the same type can conflict.

## Child process credential boundary

Reeve constructs the environment for every Pulumi, Terraform, OpenTofu, and policy subprocess.
It does not copy the controller process environment into those commands.

The constructed environment contains:

- Ambient values for `COLORTERM`, `FORCE_COLOR`, `LANG`, `LANGUAGE`, `NO_COLOR`, `PATH`, `SSL_CERT_DIR`, `SSL_CERT_FILE`, `TEMP`, `TERM`, `TMP`, `TMPDIR`, `TZ`, and every `LC_*` key.
- `HOME`, `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`, and `XDG_DATA_HOME` from an isolated CI home or the existing local environment.
- Credentials selected by auth bindings for the current stack and mode.
- Credentials selected by `engine.state.auth_provider` for backend access.
- `PULUMI_CONFIG_PASSPHRASE` when `engine.state.secrets_provider.type` is `passphrase` and a configured value exists.
- A state auth provider may supply the variable through a secret manager or acknowledged `env_passthrough` mapping.
- Reeve never copies the controller's ambient passphrase or expands an environment reference from engine config.
- `TF_IN_AUTOMATION=1` for Terraform and OpenTofu commands.
- `PULUMI_EXPERIMENTAL=true` for Pulumi saved-plan commands.

CI engine processes receive a private temporary `HOME` and XDG directories that are deleted when the run ends.
Local runs retain the operator's home paths so local CLI profiles continue to work.

Preview, refresh, and drift resolve state authentication before invoking an engine.
Apply waits until approvals, checks, preview, lock, freeze, fork, draft, and policy gates pass before resolving state credentials or running Pulumi login.

Stack credentials override state credentials when both explicitly provide the same environment key.

Credentials are reused within one invocation, refreshed when needed, and cleaned up when the command finishes.
[Invocation reuse](../openspec/specs/auth/spec.md#invocation-reuse) specifies concurrency and expiry behavior.

This boundary prevents accidental ambient inheritance but is not an operating-system sandbox.
Run approved untrusted code under a separate user, container, VM, or job boundary.

---

## AWS OIDC (`aws_oidc`)

Short-lived STS credentials via `AssumeRoleWithWebIdentity`. Requires
GitHub Actions' OIDC provider (`id-token: write` permission).

```yaml
providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111111111111:role/reeve-prod
    session_name: reeve-prod                    # default: "reeve"; sent verbatim as the STS RoleSessionName
    duration: 1h                                # default 1h; lint warns >4h
    region: us-east-1
    audience: sts.amazonaws.com                 # default; override for custom audiences
```

### IAM setup

One-time cloud setup (outside reeve):

1. Create the OIDC provider in the target AWS account:

   ```text
   URL:       https://token.actions.githubusercontent.com
   Audience:  sts.amazonaws.com
   Thumbprint: (AWS console will populate)
   ```

2. Create an IAM role with a trust policy keyed on your repo + ref:

   ```json
   {
     "Version": "2012-10-17",
     "Statement": [{
       "Effect": "Allow",
       "Principal": {
         "Federated": "arn:aws:iam::111111111111:oidc-provider/token.actions.githubusercontent.com"
       },
       "Action": "sts:AssumeRoleWithWebIdentity",
       "Condition": {
         "StringEquals": {
           "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
         },
         "StringLike": {
           "token.actions.githubusercontent.com:sub": "repo:myorg/myrepo:*"
         }
       }
     }]
   }
   ```

3. Attach the permissions the stack actually needs. For a drift role,
   use read-only IAM policies.

Exported env vars: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
`AWS_SESSION_TOKEN`, `AWS_REGION`, `AWS_DEFAULT_REGION`.

---

## GCP Workload Identity Federation (`gcp_wif`)

Exchange GitHub OIDC → STS federated token → service account impersonation
token. Emits an ambient credentials file at `GOOGLE_APPLICATION_CREDENTIALS`
plus `CLOUDSDK_AUTH_ACCESS_TOKEN`.

```yaml
providers:
  gcp-prod:
    type: gcp_wif
    workload_identity_provider: projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/reeve
    service_account: reeve-prod@PROJECT_ID.iam.gserviceaccount.com
    duration: 1h
```

The credential exchange rejects missing tokens, malformed expiry, and expired credentials before engine execution.
Implementation details and exchange contracts live in the [auth specification](../openspec/specs/auth/spec.md).

### GCP setup

```bash
# Create the pool and provider
gcloud iam workload-identity-pools create github \
  --location=global --display-name="GitHub Actions"

gcloud iam workload-identity-pools providers create-oidc reeve \
  --workload-identity-pool=github --location=global \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository=='myorg/myrepo'"

# Let the service account be impersonated by workflows in this repo
gcloud iam service-accounts add-iam-policy-binding \
  reeve-prod@PROJECT_ID.iam.gserviceaccount.com \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/myorg/myrepo"
```

---

## Azure federated identity (`azure_federated`)

```yaml
providers:
  azure-prod:
    type: azure_federated
    tenant_id: ${env:AZURE_TENANT_ID}
    client_id: 44444444-4444-4444-4444-444444444444
    subscription_id: 55555555-5555-5555-5555-555555555555
    audience: api://AzureADTokenExchange     # default
    duration: 1h
```

### Azure setup

1. Create an App Registration in Entra ID.
2. Add a federated credential:

   ```text
   Issuer:    https://token.actions.githubusercontent.com
   Subject:   repo:myorg/myrepo:ref:refs/heads/master
   Audience:  api://AzureADTokenExchange
   ```

3. Grant the app the RBAC roles your stacks need on the subscription or
   resource group.

Exported env vars: `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`,
`AZURE_SUBSCRIPTION_ID`, `AZURE_ACCESS_TOKEN`, `ARM_*` mirrors, and
`AZURE_USE_OIDC=true` for Pulumi's Azure provider.

---

## GitHub App (`github_app`)

Exchanges the app's private-key JWT for a short-lived installation token.

```yaml
providers:
  github-app:
    type: github_app
    app_id: 123456
    installation_id: 789012
    private_key: ${env:GITHUB_APP_PRIVATE_KEY}   # or a file path
```

`private_key` accepts three forms:

1. Literal PEM (starts with `-----BEGIN`)
2. File path
3. Base64-encoded blob (Actions secrets often deliver it this way)

A bound `github_app` provider exports a token to the engine, with permissions governed by the App installation.
For the identity that posts Reeve comments, follow [controller App setup](github-actions.md#github-app-identity) instead.

---

## Secret managers

Secret-manager providers retrieve a value using the controller's available credentials, then map it into engine variables with `env_map`.
The parsed `source:` field does not currently wire a parent auth provider into retrieval.

| Provider | Retrieval credential in the controller |
| --- | --- |
| AWS Secrets Manager / SSM | AWS SDK default credential chain. |
| GCP Secret Manager | `CLOUDSDK_AUTH_ACCESS_TOKEN` in the controller environment. |
| Azure Key Vault | Azure SDK `DefaultAzureCredential`. |
| GitHub secret | The explicitly named controller environment variable. |

A sibling federated workload binding does not populate the controller's ambient environment.
Prepare retrieval credentials explicitly; the shared workflow's GCP ADC setup alone does not promise the access-token environment variable required by `gcp_secret_manager`.

### `env_map`

`env_map` maps **env var name → field inside the fetched secret** and is
required on remote secret-manager providers — without it the provider would
fetch the secret and export nothing, so `reeve lint` rejects the config.
For `github_secret`, omitting `env_map` passes `env_var` through unchanged.

```yaml
env_map:
  CLOUDFLARE_API_TOKEN: ""          # "" = whole secret (plain-string secrets only)
  DATADOG_API_KEY: api_key          # extract "api_key" from a JSON secret
```

Semantics (fail closed):

- An empty field value (`""`) exports the **whole secret**, and is only
  valid when the secret is a plain string. If the secret is a JSON
  object (a credential bundle), reeve refuses with a hard error — name
  the field instead. The whole bundle is never exported implicitly.
- A named field must exist as a string in the JSON secret. A missing or
  non-string field is a hard error naming the field — never a silent
  fallback to the whole secret.
- Every exported value is registered with the central redactor, so a
  secret that leaks into engine output is scrubbed from logs and PR
  comments.

### AWS Secrets Manager

```yaml
providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111:role/reeve-prod
    region: us-east-1

  cloudflare-token:
    type: aws_secrets_manager
    secret_id: reeve/cloudflare/api-token
    region: us-east-1
    ttl: 1h
    env_map:
      CLOUDFLARE_API_TOKEN: ""      # "" = whole (plain-string) secret value
```

The controller identity used for retrieval needs `secretsmanager:GetSecretValue` on the secret ARN.
The returned secret can be long-lived; Reeve does not rotate it.

### AWS SSM Parameter

```yaml
providers:
  datadog-key:
    type: aws_ssm_parameter
    parameter: /reeve/datadog/api-key
    region: us-east-1
    env_map:
      DATADOG_API_KEY: ""
```

### GCP Secret Manager

Requires `CLOUDSDK_AUTH_ACCESS_TOKEN` in the controller environment.
A `gcp_wif` workload binding alone does not provide that controller variable.

```yaml
providers:
  stripe-key:
    type: gcp_secret_manager
    name: projects/PROJECT_ID/secrets/stripe-api-key/versions/latest
    env_map:
      STRIPE_API_KEY: ""
```

### Azure Key Vault

Uses `DefaultAzureCredential` - the pod / process needs a federated or
managed-identity token in scope.

```yaml
providers:
  sendgrid-key:
    type: azure_key_vault
    vault_name: mycompany-prod-kv
    secret_name: sendgrid-api-key
    env_map:
      SENDGRID_API_KEY: ""
```

### GitHub secret (env-backed)

For secrets that GitHub Actions already surfaces as env vars (from the
workflow's `env:` or `secrets` wiring):

```yaml
providers:
  cloudflare-token:
    type: github_secret
    env_var: CLOUDFLARE_API_TOKEN     # exported with this same name by default

  custom-token:
    type: github_secret
    env_var: MY_CUSTOM_SECRET
    env_map:
      MY_TOOL_TOKEN: ""             # re-export under the name the engine expects
```

In a custom composite-action job (not a reusable-workflow caller):

```yaml
- uses: reeveops/reeve@d31c814640689c2f2e1b0d02d2bc11a80a94faab
  env:
    MY_CUSTOM_SECRET: ${{ secrets.MY_CUSTOM_SECRET }}
```

---

## Local development

`aws_profile`, `aws_sso`, and `gcloud_adc` **refuse to run when `CI=true`**.
This is a hard refusal with no CLI override - if you need long-lived keys
in CI, you've stepped off the zero-trust path and should reach for
`env_passthrough` (which is loudly flagged).

```yaml
providers:
  aws-local:
    type: aws_profile
    profile: mycompany-dev
    region: us-west-2

  gcp-local:
    type: gcloud_adc
```

Use these for `reeve plan-run` / `reeve run preview --local` against live
cloud without going through OIDC.

### Binding local providers (`local:`)

OIDC providers (`aws_oidc`, `gcp_wif`, `azure_federated`) exchange a
GitHub Actions token and can never acquire on a laptop. To run locally
against a stack bound to them, name a local substitute on the binding:

```yaml
bindings:
  - match: { stack: "*/prod" }
    providers: [gcp-prod]        # gcp_wif - used in CI
    local: [gcp-local]           # gcloud_adc - used in --local runs only
```

In a `--local` run, each `local:` entry replaces the resolved providers of
the same logical scope (same rule as `override:`). Everything else - a
`gcp_secret_manager` provider on the same stack, say - passes through
unchanged. CI runs never consult `local:`.

Rules enforced by `reeve lint`:

- `local:` entries must reference declared providers.
- `local:` entries must not be CI-only OIDC types - such an entry is dead
  config.

### `--local-auth`

To substitute without touching config, pass declared provider names on the
command line; each replaces same-scope resolved providers for every stack,
after any `local:` lists (the flag wins):

```bash
reeve plan-run --local-auth gcp-local,aws-local
```

The flag requires `--local` (`plan-run` and `render` imply it).

---

## `env_passthrough` - the flagged escape hatch

Use `env_passthrough` only when federation or a supported secret-manager
provider is not available, such as airgapped CI or a legacy provider. It
maps selected variables from the Reeve host process into the IaC engine
process:

```yaml
providers:
  legacy-snowflake:
    # Explicitly opt out of short-lived federation or secret-manager retrieval.
    type: env_passthrough

    # Required acknowledgement: these may be long-lived credentials and will
    # be made available to repository-controlled IaC code.
    i_understand_this_is_dangerous: true

    # engine environment variable: host environment variable
    env_vars:
      SNOWFLAKE_USER: SNOWFLAKE_USER
      SNOWFLAKE_PASSWORD: SNOWFLAKE_PASSWORD
```

The `env_vars` mapping is deliberately explicit. Its direction is
**engine variable → host variable**: the example reads
`SNOWFLAKE_PASSWORD` from Reeve's host environment and exports it to the
engine under the same name. Reeve does not copy its complete host
environment. The provider is acquired only when a binding selects it for
the current stack and run mode.

### Why the acknowledgement is required

Setting `i_understand_this_is_dangerous: true` acknowledges two separate
risks:

1. **Credential lifecycle and authority.** The supplied value may be a
   long-lived credential. Reeve does not create it, reduce its permissions,
   rotate it, attach an expiry, or revoke it after the run. Passing it
   directly to the engine bypasses Reeve's preferred short-lived,
   per-stack federation model.
2. **Exposure to executed code and output.** The mapped value becomes
   available to the IaC engine and the repository-controlled code,
   providers, and plugins it executes. Any of them could include the value
   in stdout, stderr, an error, or a generated artifact.

Reeve mitigates the second risk by registering every mapped credential
value with its central redactor. If an engine prints the exact value,
Reeve replaces it with `[redacted]` before the output reaches PR comments,
audit logs, run artifacts, or telemetry. This is why credentials should
cross the auth-provider boundary instead of being inherited ambiently:
Reeve can contain the environment and knows which literal values must be
treated as secrets.

Redaction is defense in depth, not a security boundary:

- Only explicitly mapped values are known to the redactor.
- Literal matching cannot guarantee masking after a value is encoded,
  hashed, truncated, interpolated, or otherwise transformed.
- Values shorter than eight characters are not registered for literal
  masking because they would over-redact ordinary output.
- The setting does not make a credential short-lived, least-privileged, or
  otherwise safer.
- `env_passthrough` is not a local-only provider and is not refused when
  `CI=true`.

### Terraform and OpenTofu variables

Terraform and OpenTofu commonly consume input variables through
`TF_VAR_<name>`. Those values can use this provider too:

```yaml
providers:
  terraform-legacy:
    type: env_passthrough
    i_understand_this_is_dangerous: true
    env_vars:
      # Terraform consumes TF_VAR_database_password; the host workflow
      # supplies it as DATABASE_PASSWORD.
      TF_VAR_database_password: DATABASE_PASSWORD
```

Because `DATABASE_PASSWORD` crosses the auth-provider boundary, its
literal value is registered with Reeve's redactor. Terraform's own
`sensitive = true` metadata remains important, but it is separate from
Reeve's masking: a value Terraform obtains or derives through another path
may never become known to Reeve.

Every acquisition emits a warning, and `reeve lint` rejects the provider
when `i_understand_this_is_dangerous` is absent or false. The provider
also checks the acknowledgement at acquisition time, so skipping lint
does not bypass it. If a mapped host variable is missing, Reeve warns and
does not export that engine variable; the downstream engine may then fail
because its credential is incomplete.

**Before using this provider, ask:**

- Does the service support OIDC or another federated credential exchange?
- Can the value live in AWS Secrets Manager, GCP Secret Manager, Azure Key
  Vault, or another supported provider so rotation stays outside the
  repository workflow?
- Is the credential scoped to the minimum permissions and lifetime
  available?

---

## Fork PR policy

`apply.allow_fork_prs` defaults to `false` and blocks apply and writing refresh on fork PRs.
It does not automatically reduce IAM permissions or create a special read-only credential for preview.

Preview uses the configured preview bindings when workflow permissions and credentials allow it to run.
Use explicit read-only preview roles and a suitable execution boundary for untrusted code; GitHub's event/token restrictions also affect what a fork workflow can access.

```yaml
apply:
  allow_fork_prs: false
```

Enabling this setting permits fork applies to reach the other gates; it does not remove approval, checks, policy, or lock requirements.
Do not use a label or manual dispatch as a substitute for reviewing the code and credential boundary.

---

## Troubleshooting

### `ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not set`

You're running an OIDC provider (`aws_oidc`, `gcp_wif`, `azure_federated`)
outside GitHub Actions or without `permissions: id-token: write`.

For local dev, declare an `aws_profile` / `gcloud_adc` provider and bind
it with a `local:` list on the binding (or pass `--local-auth <name>`) -
see [Local development](#local-development).

### `lint`: "conflicting providers of scope aws: aws-a vs aws-b"

Two `aws_oidc` providers are bound to the same stack. Merge into one
binding that lists a single AWS role, or narrow one binding's pattern.

### `stsc 403 AccessDenied` on AssumeRoleWithWebIdentity

Trust policy mismatch. Check:

- `token.actions.githubusercontent.com:aud` condition matches your
  provider's `audience:` (default `sts.amazonaws.com`).
- `:sub` condition matches the workflow's ref. Use `StringLike` with
  `repo:myorg/myrepo:*` to cover all refs, or tighten to specific branches.

### `gcp_wif` returns empty access token

Check the provider response, mapped claims, repository/ref condition, service-account binding, and API permissions.
Correct the mismatched claim or binding without removing the trust restriction.

### GitHub App 404 on `/app/installations/{id}/access_tokens`

`installation_id` doesn't match the app's install on your org. List
installations with:

```bash
curl -H "Authorization: Bearer $(make_jwt.sh)" \
  https://api.github.com/app/installations
```
