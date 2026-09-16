# Getting started

Zero to PR-comment in ten minutes. This guide walks you through the minimum
setup: one Pulumi project, one stack, a filesystem bucket (no cloud yet),
and a GitHub Actions workflow that opens a PR and comments on it.

For cloud-native setups (S3 locks, OIDC federation, multi-stack monorepo)
see [configuration.md](configuration.md) and [auth.md](auth.md).

## Prerequisites

- A GitHub repo with a Pulumi project. The [`examples/toy-stack/`](../examples/toy-stack/)
  in this repo is a working one - fork and start from there if you want.
- GitHub Actions enabled.
- Optional: a running Pulumi backend. The toy stack uses the `random`
  provider so no cloud credentials are needed.

## 1. Install reeve locally

Grab a prebuilt tarball from the
[releases page](https://github.com/reeveops/reeve/releases) (verify its
sha256 against the release's cosign-signed `checksums.txt`), or build
from source:

```bash
git clone https://github.com/reeveops/reeve
cd reeve
mise install           # go + tooling (go, golangci-lint, govulncheck, gosec, hk)
go build -o ./bin/reeve ./cmd/reeve
./bin/reeve --help
```

Put `./bin/reeve` on your `$PATH` or invoke it directly.

## 2. Create `.reeve/` with `reeve init`

At the repo root, run:

```bash
reeve init
```

`reeve init` scans the repo for Pulumi projects and Terraform root modules
(the same scan as `reeve stacks discover`), shows what it found, and walks
you through a short wizard: the IaC engine (pulumi, terraform, or OpenTofu -
pick `tofu` explicitly, it reads the same `.tf` files as terraform),
approvals (CODEOWNERS-based or an explicit approver list), an optional
commented freeze-window example, an optional Slack notification channel, and
an approval-freshness window. Everything you skip is written as a commented
best-practice example you can enable later.

Running in a script or CI (or passing `--non-interactive` / `-n`) skips all
prompts and writes a safe baseline: engine detected from repo files, stacks
pre-filled, every optional gate off. Existing `.reeve/` files are never
overwritten - `init` only fills in missing config types unless you pass
`--force` (which keeps `*.bak` backups).

Release binaries also write `.github/workflows/reeve.yml` with the detected
engine and pin the shared workflow to the release's exact source commit.
Development builds accept the same pin through `--workflow-ref <full-commit-sha>`.

An existing workflow is preserved.

Then check the result:

```bash
reeve lint
```

### What it wrote

Two config files, the GitHub Actions caller, and `notifications.yaml` when you
configure Slack. You can also write these by hand.

**`.reeve/shared.yaml`** - bucket, approvals, preconditions:

```yaml
version: 1
config_type: shared

bucket:
  type: filesystem
  name: ./.reeve-state           # local dir for quick iteration

approvals:
  sources:
    - type: pr_review
      enabled: true
  default:
    required_approvals: 1
    approvers: ["@your-org/infra-reviewers"]
    dismiss_on_new_commit: true

preconditions:
  require_up_to_date: true
  require_checks_passing: true
  preview_freshness: 2h

apply:
  trigger: comment               # comment (default): apply on /reeve apply | merge: apply on PR merge
  allow_fork_prs: false          # deny-by-default; flip with care
  # auto_ready: true             # reserved — not yet enforced (draft→ready already notifies for approval when a plan has succeeded)
```

**`.reeve/pulumi.yaml`** - engine + stack declarations:

```yaml
version: 1
config_type: engine

engine:
  type: pulumi
  binary:
    path: pulumi

  stacks:
    - pattern: "projects/*"      # globs are doublestar
      stacks: [dev, staging, prod]

  change_mapping:
    ignore_changes:
      - "**/*.md"
      - "**/node_modules/**"

  execution:
    max_parallel_stacks: 2
    preview_timeout: 10m
```

## 3. Verify locally

```bash
reeve lint                    # strict YAML check + cross-file validation
reeve stacks                  # prints the declared-and-enumerated stacks
reeve rules explain prod/api  # shows merged approval rules for one stack
reeve plan-run --sha $(git rev-parse HEAD) --run-number 1
```

`plan-run` renders the PR comment to stdout. No cloud calls, no GitHub calls,
no external services. Filesystem artifacts land under `.reeve-state/`.

## 4. Add the GitHub Actions workflow

**`.github/workflows/reeve.yml`**:

```yaml
name: reeve

on:
  pull_request:
    types: [opened, reopened, synchronize, ready_for_review]
  merge_group:
    types: [checks_requested]
  issue_comment:
    types: [created]
  # Only add pull_request_review if you set run_on_approval: true below -
  # otherwise the action skips review events, so subscribing to them just
  # burns runner minutes.
  # pull_request_review:
  #   types: [submitted]

permissions:
  contents: read
  checks: read
  pull-requests: write
  issues: write

jobs:
  reeve:
    uses: reeveops/reeve/.github/workflows/reeve.yml@<full-commit-sha>
    with:
      mode: gitops
      pulumi_version: latest
```

Pin the workflow call to a reviewed full commit SHA.
The shared workflow owns routing, checkout, caching, tool setup, timeout, and safe preview concurrency.

Scheduled bucket cleanup uses the same workflow with `mode: maintenance` and runs no IaC engine.
It executes `reeve maintenance run` for expired locks and configured artifact retention.

Use `opentofu_version` or `terraform_version` instead of `pulumi_version` for an HCL engine.
Configure engine and state credentials through the federated or secret-manager providers in `.reeve/auth.yaml`.
`reeve init` adds `id-token: write` when the loaded config declares AWS OIDC, GCP WIF, or Azure federated auth.

It adds the `closed` pull request type only when `apply.trigger` is `merge`.

Keep the caller job ID `reeve` and require the `reeve / Reeve` check in branch protection.
Reeve derives a custom caller check name from the current run and excludes its prior results from apply gates.

That's it. The action auto-detects the command from the event:

| Event / Comment                                    | Action                   |
| -------------------------------------------------- | ------------------------ |
| `pull_request` (opened / reopened / synchronize)   | `reeve run preview`      |
| `pull_request` (ready_for_review)                  | `reeve run ready`        |
| `pull_request` (closed and merged, when enabled)   | `reeve run apply`        |
| `pull_request` (any other action: labeled, ...)    | silent no-op             |
| `/reeve ready` comment                             | `reeve run ready`        |
| `/reeve apply` comment                             | `reeve run apply`        |
| `/reeve refresh` comment                           | `reeve run refresh`      |
| `/reeve unlock [project/stack]` comment            | frees this PR's locks    |
| `/reeve explain [project/stack]` comment           | `reeve run explain` - report-only why: rules, locks, gate trace |
| `/reeve help` comment                              | posts available commands |
| Any other comment, or any bot-authored comment     | silent no-op             |

Event classification runs before binary setup, checkout, authentication, and engine installation.
Skipped events do not run those setup steps.

### Repository roots and change scope

`--root` may point at a nested infrastructure directory in the checkout.
Reeve converts repository-relative changed files to paths under that root before preview, apply, refresh, explain, and notification-policy checks.

- Changes outside the configured root select no stacks.
- Documentation-only or outside-root previews do not open blob storage, acquire engine credentials, initialize an engine session, or dispatch lifecycle notifications.
- Apply stays bound to the stack set in the preview manifest for the PR head, even if GitHub's live changed-file list moves while the PR is open.
- The maintained action checks out one immutable PR head and fails if the PR moves before gates, artifacts, apply, or refresh use it.

**`reeve run preview` exit behavior**:

- A preview exits `0` only when every targeted stack plans successfully or is a no-op.
- If any stack fails, reeve writes the manifest before exiting nonzero.
- If a PR comment client is available, reeve attempts to post the failure details.
- Reeve reports a posted comment only when the PR comment was written; local runs and runs without a comment client print the rendered comment instead.

**`reeve run apply` exit codes** (this is what turns the PR check red or
green):

| Exit | Meaning |
| ---- | ------- |
| `0`  | Every targeted stack applied cleanly or was a no-op — or every stack was **blocked** by preconditions/locks. Blocked is a deliberate non-failure: the gates held the apply back, nothing was attempted, and a later re-run can proceed. |
| `1`  | One or more stacks **failed** to apply (engine, auth, or lock-storage error), the run was cancelled by a signal, post-apply persistence failed, or the run errored before applying (config, VCS, storage). The error message names the failed stacks. A failed apply never renders as a green check. |

The reusable workflow accepts one `command_prefix` (default `"/reeve"`) so
unrelated comments skip before GitHub assigns a runner. The composite action
still accepts multiple comma-separated `command-prefix` values when used
directly. Mention style (`@reeve apply`) is **not** accepted by default because
`github.com/reeve` is a real person's account. Comments authored by bots (user type `Bot` or a
login ending in `[bot]`) are always skipped, so reeve's own PR comments can
never re-trigger a run.

> **Review approvals:** by default an approval does not trigger a run -
> approvals don't change code, and the apply gate re-checks approvals at
> apply time. If you want the automatic approved-state notification (e.g.
> the Slack "ready to apply" update) the moment a PR is approved, set
> `run-on-approval: "true"` on the action and subscribe the workflow to
> `pull_request_review: types: [submitted]`.

> **Draft PRs:** apply is blocked. reeve returns an error if `/reeve apply`
> is attempted on a draft PR.
> When a draft PR is converted to ready for review, reeve automatically notifies for
> approval if a plan has already succeeded for the head commit.

Open a PR. reeve posts a comment within ~30 seconds showing the plan for
every stack touched by the changed files.

### Pinning and binaries

The `uses:` ref decides where the action gets its `reeve` binary. A cache
keyed by the action repository, build variant, platform, and source hash comes
first. On a cache hit nothing is downloaded or built:

- **`@vX.Y.Z[-prerelease]`** - downloads that release's signed tarball and verifies it
  against the release's `checksums.txt`.
- **`@master` / `@next`** - downloads the per-push prerelease whose signed
  source hash matches the action source already on disk.
- **A full commit SHA** - downloads that commit's retained prerelease when its
  signed source hash matches, then falls back to a source build when unavailable.
- **Anything else** (a feature branch or fork) - builds from source on the
  runner. Any missing asset or verification failure also falls back safely.

The prebuilt paths skip the Go toolchain setup + compile, saving ~30s+ on
first runs and cache misses.
The action publishes a verified download or local build before workload code runs.

## 5. Move the bucket to real storage

Filesystem buckets work great for smoke tests but every CI run starts fresh,
so lock state is lost. Switch to S3 / GCS / Azure Blob before enabling
`apply`.

Change `.reeve/shared.yaml`:

```yaml
bucket:
  type: s3                 # or gcs | azblob | r2
  name: mycompany-reeve
  region: us-east-1
```

Commit, push, and the next PR run will write locks and artifacts to the
real bucket. See [self-hosting.md](self-hosting.md) for bucket provisioning
recipes.

## 6. Add federated auth for the engine

When you move from the toy stack to real infrastructure, you need short-lived
cloud credentials for `pulumi apply` to run. See [auth.md](auth.md) -
the three-minute version:

**`.reeve/auth.yaml`**:

```yaml
version: 1
config_type: auth

providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111111111111:role/reeve-prod
    region: us-east-1
    duration: 1h

bindings:
  - match: { stack: "prod/*" }
    providers: [aws-prod]
```

Set up the AWS IAM role to trust GitHub's OIDC provider for
`token.actions.githubusercontent.com` with `aud=sts.amazonaws.com` and a
sub-claim matching your repo. See [auth.md#aws-oidc](auth.md) for the
trust-policy template.

## 7. Add approvals and locks

Tighten approvals for production in `.reeve/shared.yaml`:

```yaml
approvals:
  default:
    required_approvals: 1
    approvers: ["@your-org/infra-reviewers"]
  stacks:
    "prod/*":
      required_approvals: 2
      approvers: ["@your-org/sre", "@your-org/security"]
      require_all_groups: true    # one from each group, not 2-of-any

locking:
  ttl: 4h                         # maintenance reaps expired locks
  queue: fifo
```

`ttl` bounds every lease - including holders promoted from the queue when
the previous holder releases or expires.

Locks identify their holder by **PR + run**: re-running the same workflow
run refreshes the lease, but a *second concurrent run of the same PR*
(double `/reeve apply`, workflow re-run while the first is still going) is
refused with "another run of this PR holds the lock" instead of applying
concurrently. Once the first run finishes - or its lease expires - the
next attempt proceeds normally.

`reeve locks list` inspects the live state. `reeve locks explain <stack>`
shows holder + queue. `reeve locks unlock <project/stack> --pr N` removes a
closed or abandoned PR from a lock's holder/queue (omit the stack to sweep
every lock). `reeve rules explain <stack>` shows the merged rule
resolution.

## 8. Turn on drift detection

Separate workflow, scheduled, uses a read-only IAM role:

**`.github/workflows/drift.yml`**:

```yaml
name: drift
on:
  schedule:
    - cron: "17 */4 * * *"   # every 4 hours, off the hour
  workflow_dispatch:

permissions:
  contents: read
  id-token: write            # OIDC for the read-only role
  issues: write              # for github_issue drift channel

jobs:
  drift:
    uses: reeveops/reeve/.github/workflows/reeve.yml@<full-commit-sha>
    with:
      mode: drift
      pulumi_version: "3.231.0"
      drift_schedule: prod
```

Configure schedules + channels in `.reeve/drift.yaml` - see [drift.md](drift.md).
Use `drift_pattern` for a shard or `drift_if_stale: true` to skip fresh stacks.

## Troubleshooting

- **`pulumi: executable file not found`** - install Pulumi via
  `pulumi/actions@v6` before running reeve in the same job.
- **Comment keeps duplicating instead of editing in place** - reeve finds
  its comment by the hidden HTML marker `<!-- reeve:pr-comment:v1 -->`. If
  someone manually edited the comment and stripped the marker, reeve will
  post a new one.
- **`apply` says "fork PR - apply denied"** - expected. Fork PRs get
  dry-run-only credentials by default. Opt in with
  `shared.yaml: apply.allow_fork_prs: true` if you've thought about the
  supply-chain risk.
- **`apply` says "PR is in draft"** - convert the PR to ready for review
  first. Draft PRs are always blocked from apply regardless of config.
- **OIDC token exchange fails locally** - `aws_oidc`/`gcp_wif`/
  `azure_federated` only work inside GitHub Actions (they need the
  `ACTIONS_ID_TOKEN_REQUEST_URL` env var). Use `aws_profile` / `aws_sso` /
  `gcloud_adc` for local development.

## Next steps

- [configuration.md](configuration.md) - full schema for every `.reeve/*.yaml` file
- [auth.md](auth.md) - every provider type, plus GitHub App setup
- [drift.md](drift.md) - schedules, event lifecycle, channel catalog
- [policy-hooks.md](policy-hooks.md) - wiring OPA / Conftest / CrossGuard
- [self-hosting.md](self-hosting.md) - bucket provisioning, scope, distribution
