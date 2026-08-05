# Auth providers

reeve's authentication model is **zero-trust by design**: short-lived
federated credentials only, acquired per-stack at run time, discarded
after. Long-lived cloud keys are a flagged escape hatch (`env_passthrough`)
that requires explicit opt-in.

This guide covers every provider type, the cloud-side trust setup, and
common wiring recipes.

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
  - match: { stack: "prod/*" }
    providers: [aws-prod, gcp-prod]

  # Drift-specific binding: read-only role
  - match: { stack: "prod/*", mode: drift }
    providers: [aws-prod-readonly]

  # Stack-specific override: different AWS role for payments
  - match: { stack: "prod/payments" }
    override: [aws-payments-strict]     # replaces aws-prod for this stack
    providers: [github-app]             # unions with remaining providers
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

### Logical scopes

Used to detect conflicts:

- `aws` - `aws_oidc`, `aws_profile`, `aws_sso`
- `gcp` - `gcp_wif`, `gcloud_adc`
- `azure` - `azure_federated`
- `github-identity` - `github_app`
- Secret managers and Vault do not conflict (multiple allowed).

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
    permissions: ["contents:read", "issues:write", "pull_requests:write"]
```

`private_key` accepts three forms:

1. Literal PEM (starts with `-----BEGIN`)
2. File path
3. Base64-encoded blob (Actions secrets often deliver it this way)

**Why use a GitHub App instead of `GITHUB_TOKEN`?** Higher rate limits,
works across many repos, can post as a branded account, can be granted
granular scopes. For small single-repo setups, the default `GITHUB_TOKEN`
is fine.

---

## Secret managers

All secret-manager providers use a parent auth provider for the API call
and map the returned value into env vars for the engine via `env_map`.

### `env_map` (required)

`env_map` maps **env var name → field inside the fetched secret** and is
required on every secret-manager provider — without it the provider would
fetch the secret and export nothing, so `reeve lint` rejects the config.

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
    source: aws-prod                # parent provider
    secret_id: reeve/cloudflare/api-token
    region: us-east-1
    ttl: 1h
    env_map:
      CLOUDFLARE_API_TOKEN: ""      # "" = whole (plain-string) secret value
```

The underlying IAM role (`aws-prod`) needs
`secretsmanager:GetSecretValue` on the secret ARN.

### AWS SSM Parameter

```yaml
providers:
  datadog-key:
    type: aws_ssm_parameter
    source: aws-prod
    parameter: /reeve/datadog/api-key
    region: us-east-1
    env_map:
      DATADOG_API_KEY: ""
```

### GCP Secret Manager

Requires a sibling `gcp_wif` binding so the GCP access token is in the
environment when this provider runs.

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
  custom-token:
    type: github_secret
    env_var: MY_CUSTOM_SECRET
    env_map:
      MY_TOOL_TOKEN: ""             # re-export under the name the engine expects
```

In the workflow:

```yaml
- uses: FynxLabs/reeve@master
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

Use these for `reeve plan-run --local` against live cloud without going
through OIDC.

---

## `env_passthrough` - the flagged escape hatch

When federation genuinely isn't an option (airgapped CI, legacy provider
without OIDC, etc.), reeve supports mapping arbitrary host env vars into
the engine:

```yaml
providers:
  legacy-snowflake:
    type: env_passthrough
    i_understand_this_is_dangerous: true   # REQUIRED. Lint rejects without.
    env_vars:
      SNOWFLAKE_USER: SNOWFLAKE_USER
      SNOWFLAKE_PASSWORD: SNOWFLAKE_PASSWORD
```

Every run emits a loud stderr warning. `reeve lint` errors out without
the `i_understand_this_is_dangerous` field.

**If you're reaching for this, first ask:**

- Does the provider support OIDC / federated creds? (Most do now.)
- Can you put the secret in AWS Secrets Manager / GCP SM / Azure KV and
  use the corresponding secret-manager provider? That at least keeps
  the secret rotation on the cloud side.

---

## Fork PR policy

Fork PRs get **dry-run-only credentials by default**. A fork PR means
someone outside the repo's collaborators is proposing code, and running
that code with production credentials is a textbook supply-chain hole.

Opt in explicitly:

```yaml
# .reeve/shared.yaml
apply:
  allow_fork_prs: true   # read the docs before flipping this
```

With opt-in, fork PRs get the full credential set. Without, the fork-PR
precondition gate denies apply (but preview still runs with dry-run creds).

Recommended pattern: leave `allow_fork_prs: false`, use a required label
(`needs-credentials`) applied by a trusted reviewer to re-run the
workflow as a `workflow_dispatch` against the PR's head ref.

---

## Troubleshooting

### `ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN not set`

You're running an OIDC provider (`aws_oidc`, `gcp_wif`, `azure_federated`)
outside GitHub Actions or without `permissions: id-token: write`.

For local dev, use the `aws_profile` / `gcloud_adc` providers.

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

Workload identity pool's attribute condition doesn't match. Debug by
temporarily removing `--attribute-condition` and retrying; the failing
condition is usually repo or ref mismatch.

### GitHub App 404 on `/app/installations/{id}/access_tokens`

`installation_id` doesn't match the app's install on your org. List
installations with:

```bash
curl -H "Authorization: Bearer $(make_jwt.sh)" \
  https://api.github.com/app/installations
```
