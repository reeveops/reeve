# Drift detection

Drift is a **third run mode** alongside preview and apply - same discovery
pipeline, same auth bindings (with optional drift-specific overrides),
same bucket. Different trigger (scheduled), different urgency model
(alerts, not reviews).

## Mental model

A drift check asks: *"does the real infrastructure match the state the
last apply wrote?"* For Pulumi, that's `preview --expect-no-changes` with
`refresh` on first. For Terraform and OpenTofu, it's
`plan -refresh-only -detailed-exitcode`: the refresh-only plan compares
state against live infrastructure without writing state (the drifted
resources come from the plan's `resource_drift`, so
`refresh_before_check` needs no separate refresh step). Any non-zero
change count on a stack means drift.

reeve classifies each check into one of four events based on the prior
state file:

| Event | Meaning |
|---|---|
| `drift_detected` | New drift - not previously drifted (or fingerprint changed) |
| `drift_ongoing` | Still drifted since the last run. **Silent by default.** |
| `drift_resolved` | Was drifted, now clean |
| `check_failed` | Run-level error (auth, network, engine crash) |
| `check_recovered` | First successful check after a failed one — the all-clear for `check_failed` |

`check_recovered` is emitted *alongside* the run's classification (it can
accompany `drift_detected`, `drift_resolved`, or a silent no-change run),
so stateful channels can resolve the incident/issue a `check_failed`
opened. Subscribing to `check_failed` on the `pagerduty` or
`github_issue` channel implicitly subscribes `check_recovered` too.

**`drift_ongoing` is silent on purpose** - without the event lifecycle,
alerting either spams every run or fires once and goes stale. The
runner still updates state and emits OTEL metrics; only the channel
dispatch is suppressed.

## CLI

```bash
reeve drift run                        # execute a drift check on default scope
reeve drift run --pattern "prod/*"     # narrow to a glob
reeve drift run --schedule prod        # run a named schedule from drift.yaml
reeve drift run --if-stale             # skip stacks within the freshness window

reeve drift bootstrap                  # record current state as the baseline (no events)

reeve drift status                     # print last-known state for every stack
reeve drift status --stack prod/api    # limit to one stack

reeve drift report                     # render the latest report.md from the bucket
reeve drift report --format json       # same run as JSON (manifest + per-stack results)

reeve drift suppress add prod/api --until 7d --reason "known upstream change"
reeve drift suppress list
reeve drift suppress clear prod/api
```

`--schedule` must name a schedule declared in `drift.yaml`; an unknown
name is an error (listing the configured names) rather than a silent
fall-back to the global scope. `--until` accepts Go durations plus day
and week units (`48h`, `7d`, `2w`).

## Config (`.reeve/drift.yaml`)

```yaml
version: 1
config_type: drift

scope:
  include_patterns: ["prod/*", "staging/*"]
  exclude_patterns: ["*/scratch", "experiments/*"]

behavior:
  refresh_before_check: true       # default for drift (off for PR preview)
  max_parallel_stacks: 8
  timeout_per_stack: 15m           # wall-clock bound per stack attempt; unset = no bound
  retry_on_transient_error: 2      # 0 (default) = no retries

  # timeout_per_stack caps a single stack's check attempt so one hung engine
  # invocation can't stall the run. On overrun the engine process is cancelled
  # and the stack is classified as a check error (check_failed) with the reason
  # "stack check exceeded timeout_per_stack=15m"; the run continues with the
  # other stacks. A timeout is a run error, NOT a transient - it is never
  # retried, and because it bounds each attempt it also caps every retry.

  # Flap damping (unset = off): after a drift alert goes out for a stack,
  # further alerts stay silent until the drift resolves or this window
  # elapses. See "Flap damping" below. Extended durations OK (24h, 3d, 1w).
  renotify_after: 24h

  # "Transient" = a network error reaching the engine or a cloud SDK, or
  # expired credentials. A network error is retried up to this many times;
  # expired credentials trigger a single rebind (re-resolve auth) + retry,
  # bounded by the same budget. NOT retried: engine crash, plan-parse error,
  # policy failure. A stack that succeeds on a retry is not an error; one
  # that exhausts its retries classifies as `error` (fires `check_failed`).
  # Context cancellation (Ctrl-C / SIGTERM) stops retrying immediately.

  # Exit code control: when a condition below is true and occurred this
  # run, `reeve drift run` exits nonzero (naming the condition) so CI can
  # gate on it. All three default to false = always exit 0.
  #   drift_detected -> any stack fired the drift_detected event
  #   drift_ongoing  -> any stack fired the drift_ongoing event
  #   run_error      -> any check failed (check_failed / outcome error)
  exit_on:
    drift_detected: false          # don't fail CI on drift - alert instead
    drift_ongoing: false
    run_error: true                # do fail CI on run-level errors

  state_bootstrap:
    mode: require_manual           # baseline | alert_all | require_manual
    baseline_max_age: 7d           # reserved — parsed but not yet enforced

classification:
  ignore_properties:
    - resource_type: "aws:ec2/instance:Instance"
      properties: ["tags.LastScanned", "tags.AutoManaged"]
  ignore_resources:
    - "urn:*:aws:autoscaling/group:*::*autoscaler-managed*"
  treat_as_drift:
    orphaned_state: true           # tracked in state, gone from the cloud
    missing_state: true            # present in the cloud, untracked by state

freshness:
  enabled: true
  window: 4h                       # skip stacks checked within 4h
  respect_failures: true           # reserved — not yet enforced; failed stacks are always re-checked

schedules:
  critical:
    patterns: ["prod/payments", "prod/auth"]
  prod:
    patterns: ["prod/*"]
    exclude_patterns: ["prod/payments", "prod/auth"]   # covered by "critical"
  slow-movers:
    patterns: ["dev/*", "experiments/*"]

channels:
  - type: slack
    channel: "#infra-drift"
    on: [drift_detected, check_failed]

  - type: pagerduty
    integration_key: ${env:PD_CHANGE_EVENTS_KEY}
    on: [drift_detected]
    severity_map:
      prod: error
      staging: warning
      dev: info

  - type: github_issue
    on: [drift_detected]
    labels: [drift, infra]
    assignees: ["@org/sre"]

  - type: webhook
    name: incident-system
    url: https://api.incident.io/v2/alert_events/http/${env:INCIDENT_IO_TOKEN}
    on: [drift_detected]
    headers:
      Content-Type: application/json
```

## Flap damping (`behavior.renotify_after`)

A stack that oscillates drifted → clean → drifted (an upstream job that
periodically mutates and reverts something, an autoscaler fighting your
config) fires a fresh `drift_detected` + `drift_resolved` pair every
cycle. `behavior.renotify_after` bounds that noise. reeve tracks when a
drift alert for each stack last actually went out
(`last_notified_at` in the state file) and applies these rules:

- **Unset (default):** no damping - every new detection notifies, every
  resolution notifies. Exactly the behavior before this option existed.
- **Set (e.g. `24h`, `3d`):**
  - A new `drift_detected` within the window of the last alert is
    **silenced** - the flap doesn't re-page anyone.
  - Ongoing drift stays silent until the window elapses since the last
    alert, then **re-alerts as `drift_detected`** (so channels
    subscribed to detections re-trigger their incident) and restarts
    the window.
  - `drift_resolved` is delivered **once per notified episode**: if the
    drift episode being resolved never alerted (it was a damped flap),
    the recovery notice is suppressed too - channels never saw the
    detection, so there is nothing to resolve.

Damping affects **notification delivery only**. Classification events,
the drift report, `exit_on` behavior, and OTEL metrics all still see
every detection - a damped flap still fails CI when
`exit_on.drift_detected: true`.

`check_failed` / `check_recovered` are never damped.

## Classification (drift-noise filtering)

`classification:` filters the engine diff **before** a stack is classified,
so recurring noise never fires an alert. It needs the structured per-resource
diff the engine exposes (Pulumi `detailedDiff`, Terraform/OpenTofu
`resource_drift`); an engine that only reports a summary is left untouched
(the raw verdict stands).

- **`ignore_properties`** — per resource type, a list of property-path globs
  to ignore. Paths are dotted with array indices, matching the Pulumi
  `detailedDiff` style (`tags.LastScanned`, `config.rules[3].expression`);
  the Terraform adapter walks `before`/`after` into the same shape. If, after
  removing ignored paths, an **update** has no property changes left, the
  resource stops counting as drift. This only nullifies updates — a
  create/delete/replace is a resource-level change regardless of which
  properties differ.
- **`ignore_resources`** — address/URN globs; matching resources are excluded
  from drift entirely.
- **`treat_as_drift`** — whether resources that are **orphaned** (tracked in
  state but gone from the cloud) or **missing** (present in the cloud but not
  tracked) count as drift. Both default to `true`; set one to `false` to drop
  that category. Orphaned resources are detectable (a Pulumi `create` /
  Terraform `delete` in the drift set). **`missing_state` is best-effort:**
  neither a Pulumi `--expect-no-changes` preview nor a Terraform refresh-only
  plan discovers resources they don't already manage, so today nothing is
  categorized as missing and the flag has no effect — it is reserved for a
  future out-of-band inventory source.

Globs use `*` (any run of characters, including `:` and `/`) and `?`;
`resource_type`, `ignore_resources`, and the property paths are all matched
this way. A whole stack drifting to zero drift after filtering emits
`drift_resolved` if it was previously drifted, exactly as a genuinely clean
run would.

## Bootstrap modes

When a stack has no prior state file (first run ever, or the state file
was manually cleared), reeve needs to decide whether drift counts as
"new" or just baseline.

| Mode | Behavior |
|---|---|
| `baseline` | First run is silent - records state, emits no event. |
| `alert_all` | First run fires `drift_detected` for every drifted stack. |
| `require_manual` | Refuse to run until `reeve drift bootstrap` is explicitly run. |

**Default:** when `state_bootstrap.mode` is unset, the first run behaves
like `alert_all` - every drifted stack fires `drift_detected`. Noisy on
a large estate, but nothing is silently accepted as baseline.

**Recommended for production scopes:** set `require_manual` explicitly
(as in the sample above). This closes a security gap: an attacker who
can delete state files could otherwise rely on a silent baseline mode to
reset alerts the next time drift appears. With `require_manual`, drift
runs refuse until a human records the baseline:

```bash
reeve drift bootstrap                 # record current state, emit no events
reeve drift bootstrap --pattern "prod/*"   # or narrow the scope
```

## Scheduling

Drift runs are triggered by GitHub Actions cron workflows:

```yaml
# .github/workflows/drift.yml
name: drift

on:
  schedule:
    - cron: "17 */4 * * *"       # every 4 hours, off the hour
    - cron: "0 3 * * *"          # 3am nightly for slow-movers
  workflow_dispatch:
    inputs:
      schedule:
        description: "Schedule name from drift.yaml"
        required: false
        default: prod

permissions:
  contents: read
  id-token: write                # OIDC federation
  issues: write                  # for github_issue channel

jobs:
  critical:
    if: ${{ github.event.schedule == '17 */4 * * *' }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: FynxLabs/reeve@master
        with:
          command: "drift run"
          extra-args: "--schedule critical"

  slow-movers:
    if: ${{ github.event.schedule == '0 3 * * *' }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: FynxLabs/reeve@master
        with:
          command: "drift run"
          extra-args: "--schedule slow-movers"
```

The three scoping strategies compose:

- **Pattern sharding** (`--pattern`): run separate workflows per fleet.
- **Named schedules** (`--schedule`): free-form filter sets declared in
  `drift.yaml`.
- **Skip-if-fresh** (`--if-stale` or `freshness.enabled: true`): dedup
  across overlapping schedules.

Small teams use none of these. Large monorepos use all three.

## Drift-specific auth

Apply needs write access; drift should not. Bind a read-only role with
`mode: drift`:

```yaml
# .reeve/auth.yaml
providers:
  aws-prod:
    type: aws_oidc
    role_arn: arn:aws:iam::111:role/reeve-prod
  aws-prod-readonly:
    type: aws_oidc
    role_arn: arn:aws:iam::111:role/reeve-drift-readonly

bindings:
  - match: { stack: "prod/*" }
    providers: [aws-prod]              # apply + preview

  - match: { stack: "prod/*", mode: drift }
    providers: [aws-prod-readonly]     # replaces aws-prod for drift runs
```

Grant the read-only role:

- `*:Describe*`, `*:List*`, `*:Get*` on the resources your stacks manage.
- Explicitly **no** `*:Create*`, `*:Update*`, `*:Delete*`.

For Pulumi refresh to work, it does need read access to the state
backend too (S3 bucket / KMS key).

## Suppressions

Time-bounded silence for an expected-but-non-trivial change:

```bash
# Suppress a stack for 48 hours with a reason (audited)
reeve drift suppress add prod/api \
  --until 48h \
  --reason "INC-4271: emergency patch applied out-of-band, restoring IaC sync"

reeve drift suppress list
reeve drift suppress clear prod/api
```

`--until` accepts Go durations plus day and week units (`48h`, `7d`,
`2w`).

Active suppressions live at `drift/suppressions/{project}/{stack}.json` in
the bucket. The runner skips suppressed stacks and emits no events.

For permanent suppressions (drift you've accepted as reality), declare
in `drift.yaml`:

```yaml
permanent_suppressions:
  - stack: "prod/legacy-*"          # doublestar glob over project/stack
    reason: "Vendor-managed resources; tracked in TICKET-123"
  - stack: "prod/frozen-vpc"
    until: "2026-12-31T00:00:00Z"   # optional RFC3339 expiry; omit for permanent
    reason: "Freeze window; re-enable alerts in Q1"
```

A permanent suppression is the always-on, config-level twin of
`reeve drift suppress add`. Unlike a time-bounded store suppression (which
skips the check entirely), a permanently-suppressed stack is **still checked
and its state still recorded** — so resolution is tracked and the stack shows
up in `reeve drift status` — but its `drift_detected` / `drift_ongoing` /
`drift_resolved` events are **not dispatched** to channels. It is listed in
the report under a "suppressed" section with its reason.

`check_failed` is **never** suppressed: accepting drift on a stack must not
also hide the drift checker itself breaking (auth, network, engine crash).
`until` is an optional RFC3339 timestamp after which the suppression lapses;
omit it for a permanent suppression. An unparseable `until` is treated as
permanent (and logged) rather than silently dropping the suppression.

The store-based suppressions above (`reeve drift suppress add`) and these
config-based `permanent_suppressions` are merged at run time; a stack matched
by either is suppressed.

## Channels

Drift channels ride the shared notification-channel framework
(`internal/notify`) — the same adapters that carry PR-flow notifications.
Declare them under `channels:` in `drift.yaml` (below), or in
`notifications.yaml` with drift events in `on:`; both feed the same
dispatch. One channel implementation serves both producers — see
[notifications.md](notifications.md) for the event list, delivery
guarantees (concurrent dispatch, timeouts, retry with backoff), and how
to add a destination.

> **Renamed key:** drift.yaml's list was originally called `sinks:`.
> That spelling no longer loads — reeve errors with a pointer at
> `reeve migrate-config`, which renames it to `channels:` in place
> (or just rename the key by hand).

Every channel declares which events it wants via `on:`. The drift events are
`drift_detected`, `drift_ongoing`, `drift_resolved`, `check_failed`, and
`check_recovered` - an unknown name is a hard config error (load and
`reeve lint` both reject it, listing the valid names), and a channel whose
`on:` list is empty logs a warning because it will never fire.

### Delivery durability

The drift baseline advances *before* notifications go out, so a lost
delivery could otherwise be lost forever (the next run would compare
against the new baseline and stay silent). To close that window, every
payload is persisted as an undelivered marker in the bucket
(`drift/pending-events/<project>/<stack>/<event>.json`) before dispatch
and cleared only after every subscribed channel delivered it. The next
`reeve drift run` redelivers leftover markers ahead of its own events
(a fresh event for the same stack+event supersedes a stale pending one).

Delivery is therefore **at-least-once**: if one channel fails, the next
run redelivers to *all* channels, including ones that already succeeded.
PagerDuty (dedup keys) and github_issue (marker upserts) are idempotent;
Slack/webhook may repeat a message — a duplicate beats a silently lost
alert.

### Grouping

When a single run finds drift on many stacks, each drifted stack otherwise
produces its own message per subscribed channel. Set `grouping:` on a channel
to batch those into one message per group:

| `grouping`        | Behavior                                                        |
| ----------------- | --------------------------------------------------------------- |
| `none` (default)  | One message per drifted stack (unset behaves the same).         |
| `by_environment`  | One message per environment, listing that env's drifted stacks. |

Grouping is a delivery-layer concern only: it never changes classification,
state, `exit_on`, or which events fire - just how the resulting messages are
batched. It applies to the drift alert lifecycle (`drift_detected`,
`drift_ongoing`, `drift_resolved`). `check_failed` is **never** grouped - each
is a distinct per-stack incident.

Grouping is meaningful for `slack` and `webhook`, where a combined message
cuts noise. It is a **no-op** for channels where per-stack tracking is the
point: `github_issue` (an issue is a per-stack incident to fix and close) and
`otel_annotation` (one metric/annotation per stack regardless). An unknown
`grouping:` value is a hard config error.

### Slack

One message per run per channel, no state tracking. Use a dedicated
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
  name: incident-io
  url: https://api.incident.io/v2/alert_events/http/${env:INCIDENT_IO_TOKEN}
  on: [drift_detected]
  headers:
    Authorization: "Bearer ${env:INCIDENT_IO_TOKEN}"
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
  assignees: ["@org/sre"]
```

Requires `GITHUB_TOKEN` with `issues: write`.

### OTEL annotation

Emits an annotation event to the annotations module (Grafana / Datadog /
Dash0). See [configuration.md](configuration.md#observabilityyaml).

```yaml
- type: otel_annotation
  on: [drift_detected, drift_resolved]
```

## Reports

Every run writes three artifacts to the bucket:

- `drift/runs/<run-id>/manifest.json` - run metadata
- `drift/runs/<run-id>/results/<project>-<stack>.json` - per-stack
- `drift/runs/<run-id>/report.md` - rendered markdown report

The report is also written to `$GITHUB_STEP_SUMMARY` on every CI run -
free visibility in the Actions UI.

```bash
reeve drift report                # prints latest report.md to stdout
reeve drift report --format json  # latest run as JSON: {run_id, manifest, items}
```

The JSON output re-emits the stored artifacts for the latest run: the
run manifest plus every per-stack result, ready for `jq`.

## OTEL metrics

When `observability.yaml: otel.enabled: true`:

| Metric | Type | Labels |
|---|---|---|
| `reeve.drift.detections.total` | counter | stack, env, outcome |
| `reeve.drift.duration` | histogram | stack, env |
| `reeve.drift.stacks_in_drift` | gauge | env |
| `reeve.drift.ongoing_duration` | gauge | stack |
| `reeve.drift.runs.total` | counter | outcome |

The `stacks_in_drift` gauge + `ongoing_duration` lets you alert on
"any prod stack drifted for more than 24h" in your monitoring system
rather than inside reeve.

## Overlap with open PRs

When drift is detected on a stack that has open PRs touching its paths,
the report surfaces those PRs prominently. The raw channel payload
includes them too:

```json
"overlapping_prs": [
  {"number": 482, "opened_at": "2026-04-12T09:14:00Z", "author": "alice", "paths": ["projects/api/**"]}
]
```

Long-lived IaC PRs over drifted stacks are compounding risk - the plan
reviewers approved a week ago no longer matches reality. Incident
tooling can use `overlapping_prs` to escalate.

The scan runs once per drift run (all drifted paths in one pass over the
open PRs, capped at 100 PRs). If some PRs could not be checked (a file
listing failed, or the cap was hit), the run does **not** pretend "no
overlap": the report and manifest carry a warning naming the PR numbers
that could not be checked.

## Troubleshooting

### Every run alerts as `drift_detected`, nothing resolves

The state file's fingerprint is changing every run. That usually means
an upstream system mutates a property each check (last-scanned timestamp,
managed tag). Use `classification.ignore_properties` to exclude those.

### `drift_ongoing` never emits - is it broken?

Working as designed. Query it via OTEL (`reeve.drift.ongoing_duration`
gauge) or `reeve drift status`. Most alerting on "ongoing drift" is
better phrased as "alert when `ongoing_duration > 24h`" in your
monitoring system.

### First run floods the channel with detections

That's the default (`alert_all`) bootstrap behavior. Run
`reeve drift bootstrap` first to record the baseline silently, or run
`reeve drift suppress add` for the stacks you plan to reconcile. Setting
`state_bootstrap.mode: require_manual` prevents accidental first runs
entirely.

### Drift run fails with "first run with bootstrap=require_manual"

Expected for any scope that hasn't been bootstrapped. Record the
baseline explicitly:

```bash
reeve drift bootstrap --pattern "prod/*"
```

Subsequent drift runs compare against it; `require_manual` stays set.
