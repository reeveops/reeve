# Operations

Use this guide to diagnose runs and maintain Reeve's storage.
For normal commands and approval behavior, see [PR workflow](pull-requests.md).

## Troubleshooting

| Symptom | Inspect | Next action |
| --- | --- | --- |
| No PR comment | Actions trigger and run log. | Confirm the caller is enabled, permissions are present, and `issue_comment` workflow exists on the default branch. |
| Comment command ignored | Prefix, author type, association, event action. | Use `/reeve help` from an authorized human; bot comments are deliberately ignored. |
| No stacks selected | `reeve stacks`, configured `root`, changed paths. | Use a workload change; docs-only and outside-root changes can legitimately select none. |
| Cloud authentication fails | Whether the failure is controller bucket, engine backend, or workload access. | Follow [credential wiring](auth.md#which-credentials-go-where); a workload binding does not authenticate Reeve's own bucket. |
| OIDC token variables missing | `id-token: write` and execution environment. | Add the permission in Actions or select a local provider on a laptop. |
| Engine binary missing | Engine input and Actions setup log. | Set `pulumi_version`, `terraform_version`, or `opentofu_version` on the reusable workflow. |
| Apply blocked | `/reeve explain`, stack gate trace. | Resolve the named approval, draft, freshness, checks, freeze, or lock condition. |
| Green job but no deployment | Per-stack result and apply timeline. | A blocked or already-applied run can exit zero; use the result, not job color. |
| Engine rejects a stale saved plan | Preview age and engine state changes. | Run `/reeve preview`, inspect the new result, and obtain any required fresh approval. |
| “Plan lock unavailable” | Apply timeline and bucket access. | Apply may have re-planned; inspect the actual result and fix artifact persistence. |
| Preview history unreadable | Object key/error in the log and `/reeve explain`. | Follow [preview history recovery](#preview-history-recovery). |
| Lock held or queued | `reeve locks explain project/stack`. | Check the owning PR/run before [releasing a lock](#locks-and-queues). |
| Slack/webhook silent | Config, token, subscribed event, logs. | Check [notification suppression and delivery](notifications.md#pre-approval-channel-isolation). |
| Drift requires bootstrap | Selected scope and bucket. | Run [drift bootstrap](drift.md#bootstrap-modes) with the same configuration and credentials. |

Use `--log-level debug` for a detailed local CLI trace.
For Actions, set the reusable-workflow input `log_level: debug` and inspect the run log.

## Scheduled maintenance

Reeve has no background daemon.
Schedule maintenance independently of PR activity to reap expired locks and prune old run artifacts.

```yaml
name: reeve-maintenance
on:
  schedule:
    - cron: "43 * * * *"
  workflow_dispatch:
permissions:
  contents: read
  id-token: write # only if bucket access uses federation
jobs:
  maintenance:
    uses: reeveops/reeve/.github/workflows/reeve.yml@d31c814640689c2f2e1b0d02d2bc11a80a94faab
    with:
      mode: maintenance
      # Set root and the same bucket authentication as your GitOps caller.
```

Maintenance installs no IaC engine and runs only for scheduled/manual events.
`retention.max_age` defaults to `720h`; zero or a negative duration disables age-based pruning under `runs/`.

`locking.reaper_interval` does not schedule work.
Cloud lifecycle policies must use the actual bucket prefix and should not delete live lock or notification-state objects.

## Locks and queues

```bash
reeve locks list
reeve locks explain api/prod
reeve locks unlock api/prod --pr 123
reeve locks unlock --pr 123
```

Lock holders are identified by PR and run ID; a concurrent run of the same PR cannot adopt an unexpired holder.
Successful apply releases its locks and removes its PR from remaining queue entries.

PR-scoped removal affects only that PR's entries and does not require the administrative override policy.
An active holder is refused unless `--force` is supplied; verify the owning apply has stopped before forcing removal.

Administrative `reeve locks unlock api/prod` without `--pr` can clear another PR's holder and is governed by `locking.admin_override`.
TTL expiry can be handled on acquisition or by `reeve maintenance run` / `reeve locks reap`.

The default TTL is four hours, including promoted reservations; a running apply refreshes its lease.
If the bucket is unavailable, restore access and inspect the owning run before attempting recovery.

## Run attempts and artifacts

New CI run identities use the full commit SHA and include the provider's run attempt when available.
`--run-attempt` overrides `GITHUB_RUN_ATTEMPT` and must be a positive integer.

Retries keep separate manifests, saved plans, audit entries, and lock identities.
Legacy short-SHA history remains readable when its manifest matches the selected full SHA.

| Location | Contents |
| --- | --- |
| `runs/pr-<n>/<run-id>/manifest.json` | Run metadata and per-stack outcomes. |
| `runs/pr-<n>/<run-id>/plans/` | Opaque engine plans when saved-plan locking is enabled. |
| `runs/pr-<n>/applied/<sha>.json` | A clean apply recorded for a commit. |
| `locks/` | Current per-stack holder and queue records. |
| `audit/YYYY/MM/DD/` | Write-once apply audit records, including break-glass intent. |
| `drift/` | Drift state, reports, suppressions, and pending delivery records. |
| `notifications/` | Persisted notification state and timeline history. |

These paths are relative to your configured bucket prefix.
Saved engine plans can contain sensitive values and cannot be redacted without making them unusable; protect the bucket like an engine state backend.

## Preview history recovery

Apply refuses unreadable or malformed identity-bound history for the selected commit rather than using an older valid preview.
Ready skips its success notification; explain produces a diagnostic report.

First restore bucket access or repair the identified object from a known-good backup.
If repair is not possible, inspect and preserve the damaged evidence before deliberately removing that specific object, or push a new commit to establish a new preview identity.

Authorized [break-glass](break-glass.md) can recover unavailable preview history with explicit warnings and an audit trail.
That path targets only stacks mapped precisely by the current changed files; an ordinary absent, stale, or failed preview is not this recovery exception.

A missing saved-plan file is a different condition: [plan locking](pull-requests.md#plan-locking) can fall back to a fresh plan.
Do not treat that warning as proof that the reviewed plan was executed.

## Audit and telemetry

Apply audit entries are created with conditional write-once semantics; break-glass also requires a durable intent entry before engine work.
Use bucket-event tooling to forward records to your own audit system and configure storage permissions/retention to match your requirements.

Write-once creation by Reeve does not make the bucket tamper-proof against an administrator with deletion rights.
The [audit schema](../internal/audit/audit.go) and [break-glass guide](break-glass.md#audit-trail) describe the records.

[OpenTelemetry and annotations](configuration.md#observabilityyaml) are optional and go only to configured destinations.
Hashing stack labels hides names but preserves distinct-label count; dropping labels reduces that dimension.

## Upgrading

1. Read the release notes and choose a reviewed version or candidate commit.
2. Update the CLI and workflow pin together when adopting new functionality.
3. Run `reeve migrate-config --dry-run`, inspect changes, then run `reeve migrate-config` if needed.
4. Run `reeve lint` and preview a small change before applying.

Migrations keep `*.bak` backups and operate per configuration type.
An existing workflow is not overwritten by `reeve init`, so review caller inputs, permissions, and triggers explicitly.

## Reproduce an edge case

The [scenario index](../examples/README.md#test-harness-and-scenarios) links to the evolving `reeve-test` harness for saved plans, approvals, queues, concurrency, cancellation, maintenance, and run identity.
Check each scenario's current implementation, source pin, and recorded result; its overhaul is still in progress.
