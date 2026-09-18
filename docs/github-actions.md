# GitHub Actions

Use the maintained reusable workflow for standard GitOps, drift, and maintenance jobs.
It owns event classification, immutable PR checkout, binary setup, engine installation, and preview concurrency.

## GitOps caller

Save this as `.github/workflows/reeve.yml` in the consumer repository:

```yaml
name: reeve

on:
  pull_request:
    types: [opened, reopened, synchronize, ready_for_review]
  issue_comment:
    types: [created]

permissions:
  contents: read
  checks: read
  pull-requests: write
  issues: write
  id-token: write # needed for cloud federation; remove when unused

jobs:
  reeve:
    uses: reeveops/reeve/.github/workflows/reeve.yml@d31c814640689c2f2e1b0d02d2bc11a80a94faab
    with:
      mode: gitops
      pulumi_version: "3.262.0"
      # For GCS bucket access, configure both repository variables:
      # gcp_workload_identity_provider: ${{ vars.REEVE_GCP_WIF_PROVIDER }}
      # gcp_service_account: ${{ vars.REEVE_GCP_SERVICE_ACCOUNT }}
```

Choose one engine input: `pulumi_version`, `terraform_version`, or `opentofu_version`.
The versions in these docs are explicit examples; select a version supported by your workload and pin it.

Complete [bucket authentication](#bucket-authentication) before running this caller.
For comment-triggered commands, the workflow must exist on the default branch.

### Required checks and events

The standard caller job `reeve` publishes `reeve / Reeve` for branch protection.
A custom caller check name is derived from the current run; `self_check_names` adds exclusions for nonstandard check publishers.

| Event | Result |
| --- | --- |
| PR opened, reopened, synchronized | Preview affected stacks. |
| PR ready for review | Notify readiness when a successful preview exists. |
| PR closed and merged | Apply only when `apply.trigger: merge`; add `closed` to the caller. |
| Authorized `/reeve …` comment | Dispatch the command; ignore bot comments. |
| Approved review | Update approval notifications only with `run_on_approval: true`. |
| Other PR actions or unrelated comments | Skip. |

To enable review notifications, add `pull_request_review: {types: [submitted]}` to `on:` and `run_on_approval: true` to `with:`.
This changes notification timing, not whether approvals are checked at apply time.

The shared workflow does not implement a `merge_group` preview mode.
Do not treat a merge-queue trigger or a skipped job as validation of a synthetic merge-group commit.

## Bucket authentication

Reeve opens its own bucket with the runner's cloud SDK credentials, separately from the credentials it supplies to IaC subprocesses.
A workload binding in `auth.yaml` does not configure that controller SDK session.

| Reeve bucket | Controller setup |
| --- | --- |
| GCS | Shared-workflow `gcp_workload_identity_provider` and `gcp_service_account`, with `id-token: write`; or an already authenticated runner. |
| S3 | Runner IAM identity, or a custom composite-action job that establishes an AWS SDK session. |
| Azure Blob | Runner managed identity/default Azure SDK credentials, or a custom composite-action job. |
| R2 | Runner S3-compatible credentials and `AWS_ENDPOINT_URL_S3`, or a custom composite-action job. |
| Filesystem | Local demos or a lifecycle wholly contained in one job; not persistence across hosted runners. |

The shared workflow has no AWS/Azure/R2 credential inputs and accepts only the named secrets listed below.
Use the [AWS recipe](../examples/aws-oidc/README.md) or [GCP recipe](../examples/gcp-wif/README.md) for concrete wiring, then configure [engine authentication](auth.md).

## Inputs and secrets

The [workflow definition](../.github/workflows/reeve.yml) is the complete input contract.
These are reusable-workflow inputs, whose names use underscores:

| Input | Default | Purpose |
| --- | --- | --- |
| `mode` | Required | `gitops`, `drift`, or `maintenance`. |
| `runner` | `ubuntu-latest` | Runner label; use a prepared self-hosted runner when necessary. |
| `timeout_minutes` | `60` | Maximum job duration. |
| `root` | Checkout root | Directory containing `.reeve/`. |
| `pulumi_version`, `terraform_version`, `opentofu_version` | Empty | Install the selected CLI; empty skips installation. |
| `command_prefix` | `/reeve` | One prefix; multiple prefixes require the composite action. |
| `allowed_associations` | `OWNER,MEMBER,COLLABORATOR` | GitHub associations allowed to invoke commands. |
| `run_on_approval` | `false` | Dispatch approved-review notifications. |
| `log_level` | Empty | Override CLI logging level. |
| `self_check_names` | Empty | Additional Reeve check names excluded from apply gates. |
| `drift_schedule`, `drift_pattern` | Empty | Named schedule or stack glob; mutually exclusive. |
| `drift_if_stale` | `false` | Apply the drift freshness filter. |
| `gcp_workload_identity_provider`, `gcp_service_account` | Empty | Authenticate the runner to GCP. |

| Named secret | Purpose |
| --- | --- |
| `reeve_token` | Override the controller's default GitHub token. |
| `slack_token` | Supply the Slack bot token. |

For example, add this beside `with:` in the caller:

```yaml
secrets:
  slack_token: ${{ secrets.SLACK_BOT_TOKEN }}
```

Do not put a step-level `env:` or `steps:` block into a job that uses a reusable workflow.
Use a custom action job when a tool or credential is not supported by the caller interface.

## Multiple roots

One configured root supports one engine.
A repository can call Reeve separately for a Pulumi root and a Terraform/OpenTofu root, each with its own `.reeve/` directory and distinct bucket namespace.

```yaml
jobs:
  terraform:
    uses: reeveops/reeve/.github/workflows/reeve.yml@d31c814640689c2f2e1b0d02d2bc11a80a94faab
    with:
      mode: gitops
      root: tf
      terraform_version: "1.16.2"
```

Paths outside a root do not select its stacks; paths inside it are mapped relative to that root.
The current shared workflow groups preview concurrency by repository and PR, without the root path: simultaneous calls for different roots can cancel each other.
For independent concurrent roots, use custom composite-action jobs with a distinct preview concurrency group for each root.
The evolving [reeve-test layout](https://github.com/reeveops/reeve-test#opentofu--terraform-scenarios-tf) demonstrates separate consumer roots.

## Drift and maintenance callers

Drift callers need `contents: read`, `pull-requests: read` for overlap reporting, and `issues: write` when creating drift issues.
Grant `id-token: write` when using federation and install the selected engine.

```yaml
name: drift
on:
  schedule:
    - cron: "17 */4 * * *"
  workflow_dispatch:
permissions:
  contents: read
  pull-requests: read
  issues: write
  id-token: write
jobs:
  drift:
    uses: reeveops/reeve/.github/workflows/reeve.yml@d31c814640689c2f2e1b0d02d2bc11a80a94faab
    with:
      mode: drift
      pulumi_version: "3.262.0"
      # Add the same bucket authentication used by your GitOps caller.
```

Establish the [drift baseline](drift.md#bootstrap-modes) against the persistent bucket first.
[Maintenance](operations.md#scheduled-maintenance) uses `mode: maintenance` and installs no IaC engine.

## Composite action for custom jobs

Use the [composite action](../action.yml) for custom tool preparation, additional runner credentials, multiple prefixes, or GitHub Enterprise Server.
Its input names use hyphens rather than the reusable workflow's underscores.

```yaml
jobs:
  reeve:
    runs-on: ubuntu-latest
    steps:
      # Add the preparation your workload needs before this step.
      - uses: reeveops/reeve@d31c814640689c2f2e1b0d02d2bc11a80a94faab
        with:
          pulumi-version: "3.262.0"
```

Use the triggers and permissions from the GitOps caller and arrange bucket authentication.
Keep credentialed preparation restricted to trusted events; the action's internal classifier cannot protect an earlier custom step.

Let the action resolve and verify the PR head before executing workload code.
Never combine `pull_request_target` with execution of untrusted PR code and privileged credentials.

## GitHub App identity

The controller uses `github.token` by default; a GitHub App installation token supplied as `reeve_token` changes its GitHub API identity.
For a custom composite-action job, supply that token as `github-token` instead.

Register and install an App with the repository permissions your flow needs: Contents read, Checks read, Pull requests write, and Issues write.
Team membership resolution may require additional organization access; verify that access when using team approvers.

Mint a short-lived installation token in trusted setup, scoped to the consumer repository, and pass it to Reeve.
GitHub's [create-github-app-token action](https://github.com/actions/create-github-app-token) documents token creation and App setup; pin a reviewed commit when using it in a custom action job.
A stack-bound `github_app` provider supplies the engine's credentials and does not replace the controller token.

The [logo assets](logo.svg) can be rasterized for the App avatar.
The [reeve-test identity guide](https://github.com/reeveops/reeve-test/blob/master/e2e/github-apps.md) explains the separate App identities used by its evolving test harness, not a required two-App production setup.

## Versions and binaries

Pin a reviewed full Reeve commit for reproducible workflow behavior.
The documented candidate `d31c814640689c2f2e1b0d02d2bc11a80a94faab` includes the shared workflow; an older stable release may not include newer master features.

| Ref | Binary source on cache miss |
| --- | --- |
| `vX.Y.Z` or semantic prerelease | Release archive with verified checksum and cosign signature. |
| Full commit SHA | Retained source-matched prerelease, or a build of that source. |
| `master` / `next` | Source-matched per-push prerelease, or source build; the branch moves. |
| Other branches/forks | Source build. |

Cache identity includes the source repository/hash, variant, platform, and cache schema; cache publication precedes workload execution.
Unavailable or unverifiable downloads fall back to a source build, which still requires a functioning toolchain and dependency access.

The shared workflow uses the composite action from its own exact commit.
Its self-reference syntax requires GitHub.com and runner 2.336.0 or newer; use the composite action on GHES until that platform supports self references.

See [upgrading](operations.md#upgrading) for schema migration and [security policy](../SECURITY.md) for supported releases.
