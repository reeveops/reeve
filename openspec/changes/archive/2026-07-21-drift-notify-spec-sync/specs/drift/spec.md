# Drift — retroactive sync delta

## MODIFIED Requirements

### Requirement: Transient errors retry within a configured budget

`behavior.retry_on_transient_error` SHALL be an integer retry budget
(default `0` = no retries), replacing the earlier retry-once semantics.
"Transient" means a network error reaching the engine or a cloud SDK, or
expired credentials. A network error SHALL retry up to the budget; expired
credentials SHALL trigger a single auth rebind + retry, bounded by the same
budget. Engine crashes, plan-parse errors, and policy failures SHALL NOT be
retried. A stack that succeeds on a retry is not an error; one that
exhausts the budget classifies as `error` and fires `check_failed`.
Context cancellation SHALL stop retrying immediately. A per-stack timeout
is a run error, never a transient — it is not retried.

#### Scenario: Budget exhausted classifies as error

- **WHEN** `retry_on_transient_error: 2` and a stack's check hits network
  errors three times in a row
- **THEN** the stack classifies as `error` and `check_failed` fires

## ADDED Requirements

### Requirement: Each stack check attempt is time-bounded

`behavior.timeout_per_stack` (extended duration; unset = no bound) SHALL cap
a single stack's check attempt. On overrun the engine process SHALL be
cancelled and the stack classified as a check error (`check_failed`) with a
reason naming the timeout; the run SHALL continue with the other stacks.
Because the bound applies per attempt, it also caps every retry.

#### Scenario: Hung engine does not stall the run

- **WHEN** one stack's engine invocation exceeds `timeout_per_stack: 15m`
- **THEN** that stack reports `check_failed` with the timeout reason and the
  remaining stacks still run

### Requirement: Flap damping bounds re-alert noise

`behavior.renotify_after` (extended duration; unset = no damping) SHALL
damp drift alert delivery per stack, tracked via `last_notified_at` in the
stack's state file: a new `drift_detected` within the window of the last
alert is silenced; ongoing drift re-alerts as `drift_detected` when the
window elapses (restarting the window); `drift_resolved` is delivered once
per notified episode — an episode that never alerted suppresses its
recovery notice too. Damping SHALL affect notification delivery only:
classification, reports, `exit_on`, and OTEL metrics still see every
detection. `check_failed` / `check_recovered` are never damped.

#### Scenario: Flapping stack pages once per window

- **WHEN** a stack flaps drifted→clean→drifted twice within
  `renotify_after: 24h` of its first alert
- **THEN** only the first detection notifies; the flaps deliver neither
  detection nor resolution events

### Requirement: Exit codes are configurable per condition

`behavior.exit_on` SHALL make `reeve drift run` exit nonzero (naming the
condition) when the corresponding condition occurred this run:
`drift_detected` (any stack fired `drift_detected`), `drift_ongoing` (any
stack fired `drift_ongoing`), `run_error` (any check failed). All three
default to false = always exit 0. A notification-damped detection still
counts for `exit_on`.

#### Scenario: CI gates on run errors only

- **WHEN** `exit_on: {run_error: true}` and a run finds drift but no check
  fails
- **THEN** the run exits 0; if a check fails, it exits nonzero naming
  `run_error`

### Requirement: Classification filters engine noise before events fire

A `classification:` block SHALL filter the engine's structured per-resource
diff before a stack is classified, so recurring noise never alerts. It
requires the structured diff (`PreviewResult.Resources`); engines exposing
only a summary are left untouched (the raw verdict stands).

- `ignore_properties` — per resource type, property-path globs (dotted
  paths with array indices) to ignore. An **update** with no property
  changes left after filtering stops counting as drift; create/delete/
  replace remain resource-level changes regardless.
- `ignore_resources` — address/URN globs; matching resources are excluded
  entirely.
- `treat_as_drift.orphaned_state` / `.missing_state` — whether orphaned
  (tracked in state, gone from the cloud) or missing (in the cloud,
  untracked) resources count as drift. Both default true. `missing_state`
  is best-effort: with no out-of-band inventory source, nothing is
  currently categorized as missing.

Globs use `*` (any run of characters, including `:` and `/`) and `?`. A
stack filtering down to zero drift SHALL emit `drift_resolved` if it was
previously drifted, exactly as a genuinely clean run would.

#### Scenario: Noisy tag never alerts

- **WHEN** `ignore_properties` lists `tags.LastScanned` for a resource type
  and the only change on a stack is that tag on an update
- **THEN** the stack classifies as `no_drift`

### Requirement: Declarative permanent suppressions complement the store

`permanent_suppressions:` in drift.yaml SHALL declare always-on
suppressions (doublestar glob over `project/stack`, mandatory reason,
optional RFC3339 `until` expiry; unparseable `until` is treated as
permanent and logged, never silently dropped). Unlike a store suppression
(`reeve drift suppress add`, which skips the check), a permanently
suppressed stack SHALL still be checked and its state recorded — only its
`drift_detected` / `drift_ongoing` / `drift_resolved` events are withheld
from channels; the report lists it under a "suppressed" section with its
reason. `check_failed` SHALL never be suppressed. Store and config
suppressions SHALL merge at run time; a stack matched by either is
suppressed.

#### Scenario: Suppressed stack still tracks resolution

- **WHEN** a permanently suppressed stack's drift resolves
- **THEN** no event is dispatched, but `reeve drift status` and the state
  file reflect the resolution

### Requirement: Check recovery is observable

A `check_recovered` event SHALL fire when a drift check succeeds after one
or more failed checks — the all-clear for `check_failed`. Stateful channels
(pagerduty, github_issue) SHALL implicitly receive it whenever they
subscribe to `check_failed`, so their incidents/issues resolve.

#### Scenario: Incident auto-resolves

- **WHEN** a pagerduty channel subscribes to `check_failed` and a failing
  stack's next check succeeds
- **THEN** the channel receives `check_recovered` and resolves the incident

### Requirement: Notification dispatch is durable (at-least-once)

Because the drift baseline advances before notifications go out, every
payload SHALL be persisted as an undelivered marker in the bucket
(`drift/pending-events/<project>/<stack>/<event>.json`) before dispatch and
cleared only after every subscribed channel delivered. The next
`reeve drift run` SHALL redeliver leftover markers ahead of its own events;
a fresh event for the same stack+event supersedes a stale pending one.
Delivery is at-least-once: a partial failure redelivers to all channels.
Idempotent channels (pagerduty dedup keys, github_issue marker upserts)
absorb duplicates; a duplicate beats a silently lost alert.

#### Scenario: Lost delivery is not lost forever

- **WHEN** a run detects drift, advances the baseline, and crashes before a
  channel delivers
- **THEN** the next run redelivers the pending event even though the
  baseline no longer reports it as new
