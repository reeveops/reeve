# Notifications

reeve publishes lifecycle events through a single **notification-channel
framework** (`internal/notify`). Two producers feed it:

- the **PR flow** (plan → ready → approved → applying → applied/failed/blocked)
- the **drift runner** (drift_detected / drift_ongoing / drift_resolved / check_failed / check_recovered)

A destination is implemented once and can subscribe to events from either
producer: the same Slack channel that tracks a PR's apply lifecycle can also
receive drift alerts, and a webhook can be pointed at both.

## Declaring channels

Channels are declared in `.reeve/notifications.yaml` as a generic
list — `type` picks the adapter, `on:` picks the events:

```yaml
version: 2
config_type: notifications

channels:
  - type: slack
    channel: "#infra-deploys"
    auth_token: ${env:SLACK_BOT_TOKEN}
    trigger: plan                     # when the per-PR message is created
    on: [plan, approved, applying, applied, failed, blocked]

  - type: slack
    name: drift-alerts
    channel: "#infra-drift"
    auth_token: ${env:SLACK_BOT_TOKEN}
    on: [drift_detected, check_failed]

  - type: webhook
    name: audit-feed
    url: https://example.internal/hooks/reeve
    on: [applied, failed, drift_detected]
    headers:
      Authorization: "Bearer ${env:HOOK_TOKEN}"
```

Drift-only channels can also live in `drift.yaml` under `channels:` (same shape,
same adapters). drift.yaml's original spelling `sinks:` no longer loads —
`reeve migrate-config` renames it (see "Converting from the original
config" below). See [drift.md](drift.md#channels) for the drift-specific
rendering of each type.

### Events

Valid `on:` values, in lifecycle order:

| Event | Producer | Meaning |
| --- | --- | --- |
| `planning` | PR flow | Preview run started (timeline event) |
| `plan` | PR flow | Preview finished; pending approval |
| `ready` | PR flow | `/reeve ready` (or `auto_ready`) |
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
| `timeline_github` | One PR comment per commit SHA | Deployment timeline (see below). Requires `GITHUB_TOKEN` with PR write |

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
VCS-connected run, dispatch is suppressed too. Post-approval events
(`ready`, `approved`, `applying`, `applied`, …) dispatch normally — the
approval that gates them covers the config change. `--local` runs are
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

  - type: timeline_github            # heartbeat: one comment per commit SHA
```

**Slack** (`timeline_slack`): every entry is a thread reply under ONE
PR-level anchor message — no channel spam. When the dashboard `slack` channel
is also enabled, its per-PR status message *is* the anchor (both share the
per-PR blob state), and the dashboard stops posting its own terse thread
notes — the timeline's richer entries replace them. Without a dashboard
channel, the timeline creates a minimal anchor message itself.

**GitHub** (`timeline_github`): one comment per commit SHA, updated in
place as that SHA's events land:

> ### 🛰️ reeve · deployment timeline · commit `abc1234`
> - 🔍 **preview started** · 2026-07-19 12:03:05 UTC · [run](#)
> - 📋 **preview finished**: app/prod +1 ~2 -0 ±0, 1 no-op · 2026-07-19 12:04:41 UTC · [run](#)
> - 🚀 **apply started** · 2026-07-19 12:10:02 UTC · [run](#)
> - ✅ **apply finished**: app/prod +1 ~2 -0 ±0 · 2026-07-19 12:12:30 UTC · [run](#)

Pushing a new commit starts a new comment; the old SHA's history stays
intact. Timeline comments use their own marker namespace
(`reeve:timeline:v1:{sha}`) — existing status/help/apply comment markers
are untouched, so enabling the timeline never orphans an existing comment.

Entry history is persisted in the state bucket
(`notifications/pr-{n}/timeline.json`) with conditional writes, so
concurrent runs merge instead of overwriting each other.

### Delivery guarantees

- Channels receive events **concurrently** — one hung endpoint cannot starve
  the others. Each delivery is bounded by a timeout.
- HTTP channels (webhook, pagerduty) share an HTTP client with a sane
  timeout and retry transient failures (network errors, 5xx, 429) with
  bounded exponential backoff.
- Notification failures are logged, never fatal: they cannot abort a plan
  or apply.
- Notifications run last in the pipeline, so upstream failures are
  captured accurately.

## Converting from the original config

reeve is early alpha and the config shape changed: the original single
`slack:` block (and drift.yaml's `sinks:` key) no longer load — reeve
errors and points you here. Convert with one command:

```bash
reeve migrate-config            # rewrites in place; originals backed up as *.bak
reeve migrate-config --dry-run  # preview the rewrite first
```

That turns the original

```yaml
version: 1
config_type: notifications

slack:
  enabled: true
  channel: "#infra-deploys"
  auth_token: ${env:SLACK_BOT_TOKEN}
  trigger: plan
  events: [plan, applied, failed]
```

into the equivalent `channels:` entry (`events:` becomes `on:`; trigger,
icons, and rules carry over), and renames drift.yaml's `sinks:` key to
`channels:`. Hand-editing to the same shape works just as well.

`comments.*` in `shared.yaml` (PR comment rendering) is unchanged and
unrelated to channels.

## The Slack PR message lifecycle

See [configuration.md](configuration.md#notificationsyaml) for the full
message lifecycle (colors, thread timeline, trigger semantics, icons,
rules).

## Adding a destination

One interface implementation serves both producers. In
`internal/notify/channels/<name>`:

```go
func init() { notify.Register("my_channel", New) }

func New(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
    // return (nil, nil) to skip when an optional dependency is missing
}

func (s *Channel) Name() string                { ... }
func (s *Channel) Subscribes() []notify.Event  { ... } // usually notify.ParseEvents(cfg.On)
func (s *Channel) Deliver(ctx context.Context, p notify.Payload) error {
    // p.Drift != nil for drift events, p.PR != nil for PR-flow events
}
```

Then add the package to `internal/notify/all` (or import it directly in a
custom build). Channels self-register; the factory resolves purely by the
config `type:` string — no core code changes needed (see the modularity
contract in `openspec/specs/architecture`).
