# Notifications

Send PR status and drift alerts to the destinations your team already uses.
Notifications are optional; the GitHub PR remains the primary place to inspect a plan and apply result.

## Enable Slack

Create a Slack bot with access to the destination channel and message-posting permission, then store its token as `SLACK_BOT_TOKEN` in GitHub Actions secrets.
Add this to `.reeve/notifications.yaml`:

```yaml
version: 2
config_type: notifications
channels:
  - type: slack
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
    trigger: plan
    on: [plan, ready, approved, applying, applied, failed, blocked]
```

Pass the secret alongside `with:` in your reusable-workflow caller:

```yaml
secrets:
  slack_token: ${{ secrets.SLACK_BOT_TOKEN }}
```

After a successful preview, expect one PR-level message that updates as the workflow progresses.
If this PR changes notification configuration, the preview notification is [suppressed](#pre-approval-channel-isolation); test the next workload PR after merging the configuration.

## Declaring channels

A channel's `type` selects the destination and `on` selects events.
Drift-only channels may instead live under `channels:` in `.reeve/drift.yaml`; avoid declaring the same delivery twice.

### Events

Valid `on:` values, in lifecycle order:

| Event | Producer | Meaning |
| --- | --- | --- |
| `planning` | PR flow | Preview run started (timeline event) |
| `plan` | PR flow | Preview finished; pending approval |
| `ready` | PR flow | `/reeve ready` or a ready-for-review event with a successful plan |
| `approved` | PR flow | Preconditions passed; apply imminent |
| `applying` | PR flow | Apply loop started |
| `applied` | PR flow | Apply finished successfully |
| `failed` | PR flow | Apply errored |
| `blocked` | PR flow | Apply blocked (gates/locks) |
| `break_glass` | PR flow | Emergency-override apply authorized (see [break-glass.md](break-glass.md)) |
| `drift_detected` | drift | New drift on a stack |
| `drift_ongoing` | drift | Still drifted since the last run |
| `drift_resolved` | drift | Was drifted, now clean |
| `check_failed` | drift | Drift check errored |
| `check_recovered` | drift | First successful check after a failed one — the all-clear for `check_failed`. Channels subscribed to `check_failed` receive it implicitly where resolution matters (`pagerduty` resolves the incident, `github_issue` closes the issue) |

Unknown names in `on:` fail `reeve lint` / config load. A channel with an
empty `on:` list draws a warning — it will never fire (exceptions: a Slack
channel defaults to every PR-flow event at or after its `trigger`, preserving
the legacy behavior; timeline channels default to every PR-flow timeline
event, `planning` through `break_glass`).

`planning` and `break_glass` are timeline additions: they are **not** part
of the legacy Slack trigger-onward default, so existing channels'
subscriptions are unchanged unless you list them explicitly.

### Channel types

| Type | Destination | Notes |
| --- | --- | --- |
| `slack` | Slack (Web API) | PR events drive one message per PR (upsert + thread timeline); drift events post standalone messages. `channel`, `auth_token`, `trigger`, `icons`, `rules` |
| `webhook` | Generic HTTP POST | Raw JSON payload; `url`, `headers` |
| `pagerduty` | PagerDuty Events API v2 | drift: trigger/resolve per stack; PR: `failed`/`blocked` trigger, `applied` resolves. Known gap: reeve only runs on PR activity, so a PR closed or merged **without** a later successful apply leaves its incident open — resolve it in PagerDuty (dedup key `reeve-pr-<owner/repo>-<n>`). `integration_key`, `severity_map` |
| `github_issue` | GitHub issue per drifted stack | Drift events only; `labels`, `assignees`. Requires `GITHUB_TOKEN` with `issues: write` |
| `otel_annotation` | Annotation emitters (Grafana/Datadog/Dash0) | Maps drift + apply lifecycle onto annotation events; configure emitters in `observability.yaml` |
| `timeline_slack` | Slack thread under one PR-level anchor | Deployment timeline (see below). `channel`, `auth_token` |
| `timeline_github` | One PR comment per plan series | Deployment timeline (see below). Requires `GITHUB_TOKEN` with PR write |

Common fields on every channel: `type`, `name` (defaults to the type),
`enabled` (defaults to `true`), `on`.

### Pre-approval channel isolation

Previews run **automatically on the PR HEAD before any approval**, and
channel config (webhook URLs, headers, tokens) is loaded from that same
HEAD. To stop a branch pusher from adding a channel that exfiltrates
expanded credentials, pre-approval events (`planning`, `plan`) are **not
dispatched to any channel** when the PR's changed files include a
channel-bearing config file (`.reeve/notifications.yaml`,
`.reeve/drift.yaml` — derived from the files actually loaded). The
preview still runs and the PR comment carries a visible line:

> ⚠️ Notification channels suppressed for this preview: notification
> config `.reeve/notifications.yaml` modified in this PR; channels resume
> after approval/apply.

This fails closed: if the changed-file list cannot be fetched in a
VCS-connected run, dispatch is suppressed too. This suppression applies to preview events; other lifecycle commands have their own dispatch paths.
Do not treat it as a general authorization boundary for every configured destination. `--local` runs are
unaffected (no VCS interaction).

The same gate covers the OTEL exporter: when a PR modifies
`.reeve/observability.yaml`, the pre-approval preview skips OTLP
initialization entirely (no connection, no headers sent) and the PR
comment notes "Telemetry (OTEL) suppressed for this preview". Annotation
emitters need no gate — they only fire on apply/drift events.

## The deployment timeline

The dashboard surfaces above (the PR status comment, the Slack per-PR
message) are **snapshots**: edited in place to show the current state.
GitHub renders comment edits silently, so a snapshot alone can't answer
"did the preview even run for this push?". The **timeline** is the
complementary append-only activity heartbeat: one entry per lifecycle
event, each carrying the event, the short commit SHA, a timestamp, and the
CI run URL of the run that produced it (preview and apply are different
Actions runs, and each entry links its own).

Both timeline channels are **off by default** — enable them explicitly:

```yaml
version: 2
config_type: notifications

channels:
  - type: slack                      # dashboard: current status, one message per PR
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
    trigger: plan

  - type: timeline_slack             # heartbeat: every event as a thread reply
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
    # on: defaults to [planning, plan, ready, approved, applying,
    #                  applied, failed, blocked, break_glass]

  - type: timeline_github            # heartbeat: one comment per plan series
```

**Slack** (`timeline_slack`): every entry is a thread reply under ONE
PR-level anchor message — no channel spam. When the dashboard `slack` channel
is also enabled, its per-PR status message *is* the anchor (both share the
per-PR blob state), and the dashboard stops posting its own terse thread
notes — the timeline's richer entries replace them. Without a dashboard
channel, the timeline creates a minimal anchor message itself.

**GitHub** (`timeline_github`): one comment per plan series, updated in
place as that series' events land:

> ### 🛰️ reeve · deployment timeline · commit `abc1234`
> - 🔍 **preview started** · 2026-07-19 12:03:05 UTC · [run](https://github.com/acme/platform/actions/runs/123456789)
> - 📋 **preview finished**: app/prod +1 ~2 -0 ±0, 1 no-op · 2026-07-19 12:04:41 UTC · [run](https://github.com/acme/platform/actions/runs/123456789)
> - 🚀 **apply started** · 2026-07-19 12:10:02 UTC · [run](https://github.com/acme/platform/actions/runs/123456790)
> - ✅ **apply finished**: app/prod +1 ~2 -0 ±0 · 2026-07-19 12:12:30 UTC · [run](https://github.com/acme/platform/actions/runs/123456790)

A normal series starts at a plan and its first entry is that plan. A new series
means a new comment; the previous series stays as written.

A new series starts when:

- A new commit is pushed.
- A plan is explicitly requested for a commit that already has one. Pass
  `--plan-requested` on `reeve run preview` when the run came from a
  `/reeve plan` comment.

Delivery guarantees:

- A retried or re-dispatched CI job appends to the current series, so one plan
  is never split across two comments.
- Reeve correlates preview start and finish with the durable GitHub Actions run
  ID; the run URL is retained for display.
- An unmatched finish opens a recovery series instead of attaching to a newer
  overlapping plan. This is the one case where a series does not start at a
  plan.

Later series are numbered in the header; the first is unnumbered.
History persists in your bucket with conditional writes; marker and storage details live in the [notification spec](../openspec/specs/notifications/spec.md#timeline-channels).

## Delivery guarantees

- Channels receive events **concurrently** — one hung endpoint cannot starve
  the others. Each delivery is bounded by a timeout.
- HTTP channels (webhook, pagerduty) share an HTTP client with a sane
  timeout and retry transient failures (network errors, 5xx, 429) with
  bounded exponential backoff.
- Notification failures are logged, never fatal: they cannot abort a plan
  or apply.
- Start and completion events are delivered at their lifecycle points; a missing notification does not prove the engine did not run.

## The Slack PR message lifecycle

reeve sends one message per PR and edits it in place as the run progresses.
The sidebar color and status field update at each stage:

| Stage | Trigger | Color |
| --- | --- | --- |
| Plan ready | `trigger: plan` - plan finishes | 🟡 yellow |
| Ready | `/reeve ready`, or draft→ready with a successful plan | 🟡 yellow |
| Approved | Preconditions passed, apply imminent | 🔵 blue |
| Applying | Apply loop started | 🟣 purple |
| Applied | Apply completes successfully | 🟢 green |
| Failed | Apply errors | 🔴 red |
| Blocked | Preconditions not met | 🟡 yellow |

**Error rule:** if no message exists yet and apply fails, no message is created.
Errors only update an existing message.

> The Approved update can also fire the moment a PR review is approved
> (`reeve run approved`), but only if the shared workflow is configured with
> `run_on_approval: true` and the workflow subscribes to
> `pull_request_review` events. By default that dispatch is skipped - the
> apply gate re-checks approvals anyway - so Slack flips to approved at
> apply time instead.

**`/reeve apply` hint** only appears when status is `approved`. Pending-approval
states show "Waiting for approval." instead.

### Slack thread timeline

The first message opens a Slack thread. Each event appends a timestamped
timeline entry (planned, ready, approved, applying, applied, failed).
When a `timeline_slack` channel is enabled it takes over the thread with
richer entries (per-stack outcomes, per-run CI links) and these courtesy
entries are suppressed.

No plan output is sent to Slack. Full output is in the GitHub Actions run log.

Token expansion: `${env:NAME}` pulls from the process environment.

---

## Destination recipes

These channel fragments belong under `channels:` in notifications or drift configuration.
The shared workflow directly accepts the Slack token; other secrets/variables must be made available through an appropriate prepared runner or custom job, not invented reusable-workflow inputs.

### Slack

Drift messages are per stack by default; `grouping: by_environment` batches them by environment. Use a dedicated
channel (`#infra-drift`) - mixing drift with regular alerts gets noisy.

```yaml
- type: slack
  channel: "#infra-drift"
  on: [drift_detected, check_failed]
  grouping: by_environment
```

### Webhook

Generic HTTP POST with JSON body. In v1, the `raw` format is the only
shape - no named presets.

```yaml
- type: webhook
  name: incident-router
  url: https://alerts.example.com/reeve
  on: [drift_detected]
  headers:
    Authorization: "Bearer ${env:ALERT_ROUTER_TOKEN}"
```

Payload shape:

```json
{
  "event": "drift_detected",
  "project": "api",
  "stack": "prod",
  "env": "prod",
  "outcome": "drift_detected",
  "counts": {"add": 0, "change": 1, "delete": 0, "replace": 0},
  "fingerprint": "a3f8e1...",
  "error": "",
  "run_id": "drift-20260421T153000Z"
}
```

With `grouping: by_environment`, a grouped POST replaces the top-level stack
fields with the environment key and a `stacks` array:

```json
{
  "event": "drift_detected",
  "group": "prod",
  "stacks": [
    {"project": "api", "stack": "prod", "env": "prod", "outcome": "drift_detected",
     "counts": {"add": 0, "change": 1, "delete": 0, "replace": 0}, "fingerprint": "a3f8e1...", "error": ""}
  ],
  "run_id": "drift-20260421T153000Z"
}
```

Named presets for `incident_io` / `rootly` / `opsgenie` are deliberately
**not** built in. Template the payload in your webhook receiver instead -
that's where the transformation logic belongs.

### PagerDuty

Events API v2 with automatic `trigger` / `resolve` action selection.
Every stack gets two independent incident streams so a check failure
never stomps a real drift incident (and vice versa):

| Dedup key | Triggered by | Resolved by |
|---|---|---|
| `reeve-drift-<project>/<stack>` | `drift_detected`, `drift_ongoing` | `drift_resolved` |
| `reeve-drift-check::<project>/<stack>` | `check_failed` | `check_recovered` |

Subscribing to `check_failed` implicitly subscribes `check_recovered`, so
check-failure incidents always resolve once the check heals.

```yaml
- type: pagerduty
  integration_key: ${env:PD_CHANGE_EVENTS_KEY}
  on: [drift_detected, drift_resolved]
  severity_map:
    prod: error
    staging: warning
    dev: info
```

### GitHub issue

One open issue per drifted stack, identified by a hidden marker
(`<!-- reeve:drift:<project>/<stack> -->`). On re-runs, the issue body
updates. On `drift_resolved`, the issue closes.

Check failures get their own issue per stack (marker
`<!-- reeve:drift-check:<project>/<stack> -->`, title
`drift check failed: <project>/<stack>`), opened on `check_failed` and
closed on `check_recovered` — they never overwrite the drift issue.
Subscribing to `check_failed` implicitly subscribes `check_recovered`.

```yaml
- type: github_issue
  on: [drift_detected, drift_resolved]
  labels: [drift, infra]
  assignees: ["YOUR_GITHUB_LOGIN"]
```

Requires `GITHUB_TOKEN` with `issues: write`.

### OTEL annotation

Emits an annotation event to the annotations module (Grafana / Datadog /
Dash0). See [configuration.md](configuration.md#observabilityyaml).

```yaml
- type: otel_annotation
  on: [drift_detected, drift_resolved]
```

## Converting from the original config

The old `notifications.yaml` single `slack:` block and `drift.yaml` `sinks:` key no longer load.
Preview the migration before writing it:

```bash
reeve migrate-config --dry-run
reeve migrate-config
```

The converter keeps `*.bak` backups, moves Slack settings into a channel, and renames `events` to `on`.
PR comment settings under `shared.yaml: comments` are unrelated and stay unchanged.

## Adding a destination

Provider implementation belongs in the [notification specification](../openspec/specs/notifications/spec.md#adding-a-destination) and [contributor guide](../CONTRIBUTING.md).
A new adapter can serve both PR and drift events without adding setup requirements for users who do not enable it.
