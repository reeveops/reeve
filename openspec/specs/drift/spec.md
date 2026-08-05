# Drift Detection

## Third run mode

Drift is a run mode alongside preview and apply - not a separate subsystem.
Reuses stack discovery, auth bindings, engine abstraction, bucket storage,
observability.

## CLI surface

Normalized verb/noun grammar: `run | status | report | bootstrap` are verbs
at `drift` level; `suppress` is a command group.

```
reeve drift run [--pattern ... | --schedule <name> | --if-stale]
reeve drift status [--since 24h] [--stack prod/api]
reeve drift report [--format markdown|json]
reeve drift bootstrap [--pattern ...]
reeve drift suppress add <stack> [--until 48h] [--reason ...]
reeve drift suppress list
reeve drift suppress clear <stack>
```

## Run pipeline

1. Scheduled trigger invokes `reeve drift run`.
2. Stack enumeration via shared discovery pipeline (no PR context).
3. Scoping: include/exclude patterns, named schedule, `--if-stale`.
4. Auth binding resolution honors `mode: drift` overrides.
5. Engine drift check per stack:
   - Pulumi: `preview --expect-no-changes`.
   - Terraform/OpenTofu: `plan -refresh-only -detailed-exitcode`.
   Refresh before check is default-on for drift. Each attempt is bounded by
   `behavior.timeout_per_stack` (below).
6. Classification filters (below) drop configured noise from the engine's
   structured diff before the verdict.
7. Classify each stack: `no_drift | drift_detected | error | skipped_fresh`.
8. Compare against `drift/state/{project}/{stack}.json` to emit events:
   - `drift_detected` - first time drift appears (or a damping-window
     re-alert).
   - `drift_ongoing` - persistent drift (usually silent, queryable).
   - `drift_resolved` - previously drifted stack is clean.
   - `check_failed` - run-level error.
   - `check_recovered` - a check succeeds after one or more failures; the
     all-clear for `check_failed`. Stateful channels (pagerduty,
     github_issue) implicitly receive it whenever they subscribe to
     `check_failed`, so their incidents/issues resolve.
9. Write artifacts under `drift/runs/{run-id}/`; update state files.
10. Channels filter events per their `on:` rules, transform to their payload,
    deliver (durably - see below).
11. Report always rendered to `$GITHUB_STEP_SUMMARY` (free, zero-config).

## Per-stack timeout

`behavior.timeout_per_stack` (extended duration; unset = no bound) caps a
single stack's check attempt. On overrun the engine process is cancelled and
the stack is classified as a check error (`check_failed`) with a reason
naming the timeout; the run continues with the other stacks. A timeout is a
run error, **not** a transient - it is never retried, and because it bounds
each attempt it also caps every retry.

## Error taxonomy for retries

`behavior.retry_on_transient_error` is an integer retry budget (default `0`
= no retries). "Transient" means:

- Network errors reaching the engine or cloud SDK - retried up to the budget.
- Auth-expired errors - a single rebind (re-resolve auth) + retry, bounded
  by the same budget.

Not retried: engine crashes, plan-parse errors, policy failures, per-stack
timeouts. A stack that succeeds on a retry is not an error; one that
exhausts its budget classifies as `error` (fires `check_failed`). Context
cancellation (Ctrl-C / SIGTERM) stops retrying immediately.

## Flap damping

`behavior.renotify_after` (extended duration - `24h`, `3d`, `1w`; unset =
no damping) bounds re-alert noise per stack, tracked via `last_notified_at`
in the state file:

- A new `drift_detected` within the window of the last alert is silenced.
- Ongoing drift re-alerts as `drift_detected` once the window elapses since
  the last alert (so detection-subscribed channels re-trigger), restarting
  the window.
- `drift_resolved` is delivered once per *notified* episode: a damped flap
  that never alerted suppresses its recovery notice too.

Damping affects **notification delivery only**: classification, the drift
report, `exit_on`, and OTEL metrics still see every detection.
`check_failed` / `check_recovered` are never damped.

## Exit codes

`behavior.exit_on` makes `reeve drift run` exit nonzero (naming the
condition) when the condition occurred this run, so CI can gate on it. All
default to false = always exit 0:

- `drift_detected` - any stack fired `drift_detected` (damped or not).
- `drift_ongoing` - any stack fired `drift_ongoing`.
- `run_error` - any check failed (`check_failed` / outcome `error`).

## Classification (noise filtering)

`classification:` filters the engine diff **before** a stack is classified,
so recurring noise never alerts. Requires the structured per-resource diff
(`PreviewResult.Resources`, see `openspec/specs/iac`); engines exposing only
a summary are left untouched (the raw verdict stands).

- `ignore_properties` - per resource type, property-path globs (dotted
  paths with array indices, e.g. `tags.LastScanned`,
  `config.rules[3].expression`). An **update** with no property changes
  left after filtering stops counting as drift; create/delete/replace stay
  resource-level changes regardless.
- `ignore_resources` - address/URN globs; matching resources are excluded
  from drift entirely.
- `treat_as_drift.orphaned_state` / `.missing_state` - whether orphaned
  (tracked in state, gone from the cloud) or missing (in the cloud,
  untracked) resources count as drift. Both default true. `missing_state`
  is best-effort: without an out-of-band inventory source nothing is
  currently categorized as missing.

Globs use `*` (any run of characters, including `:` and `/`) and `?`. A
stack filtering down to zero drift emits `drift_resolved` if it was
previously drifted, exactly as a genuinely clean run would.

## Suppressions

Two layers, merged at run time (a stack matched by either is suppressed):

- **Store suppressions** (`reeve drift suppress add <stack> --until 48h
  --reason ...`, audited, stored at
  `drift/suppressions/{project}/{stack}.json`): the runner skips the check
  entirely and emits no events.
- **Permanent suppressions** (`permanent_suppressions:` in drift.yaml):
  doublestar glob over `project/stack`, mandatory reason, optional RFC3339
  `until` expiry (unparseable `until` = permanent, logged, never silently
  dropped). The stack is **still checked and its state recorded** - so
  resolution is tracked and it shows in `reeve drift status` - but its
  `drift_detected` / `drift_ongoing` / `drift_resolved` events are withheld
  from channels; the report lists it under a "suppressed" section with its
  reason.

`check_failed` is **never** suppressed: accepting drift on a stack must not
also hide the drift checker itself breaking.

## Delivery durability

The drift baseline advances *before* notifications go out, so a lost
delivery could otherwise be lost forever. Every payload is persisted as an
undelivered marker in the bucket
(`drift/pending-events/<project>/<stack>/<event>.json`) before dispatch and
cleared only after every subscribed channel delivered. The next
`reeve drift run` redelivers leftover markers ahead of its own events (a
fresh event for the same stack+event supersedes a stale pending one).

Delivery is therefore **at-least-once**: a partial failure redelivers to
*all* channels. Idempotent channels (pagerduty dedup keys, github_issue
marker upserts) absorb duplicates; Slack/webhook may repeat a message - a
duplicate beats a silently lost alert.

## State bootstrap

`state_bootstrap.mode` options:
- `baseline` - first run is silent, establishes baseline.
- `alert_all` - first run emits `drift_detected` for every drifted stack.
- `require_manual` - refuse to run without `reeve drift bootstrap` command.

**Unset mode behaves like `alert_all`** - noisy on a large estate, but
nothing is silently accepted as baseline. **Recommended for `prod/*` scopes
is `require_manual`**, to close the "attacker deletes state file → baseline
resets → alerts suppressed" gap. `baseline_max_age` is accepted in config but
**not yet enforced** (reserved for the `baseline` mode).

## Freshness

Before running a stack check:
- No prior state file → run (first time).
- `last_successful_check_at` older than window → run.
- No successful check recorded (previous run errored) → run (retry). Freshness
  keys on the last *successful* check, so a failed stack is always re-checked;
  `respect_failures` is accepted in config but not yet a separate toggle.
- Stack has active drift → **always run** (to detect resolution).
- Otherwise → skip, log `skipped_fresh`.

## Scoping

Three composable strategies:
- Pattern sharding (`--pattern`).
- Named schedules from config (`--schedule <name>`).
- Skip-if-fresh (`--if-stale` or config).

## Overlap with open PRs

When drift is detected on a stack with open PRs touching its paths, the
report prominently surfaces them. Raw event payloads include
`overlapping_prs: [{number, opened_at, author, paths}]`. Requires VCS
`ListOpenPRsTouchingPaths` (defined in `openspec/specs/vcs`).

## OTEL metrics

Drift-specific (stack-label cardinality gated per observability spec):
- `reeve.drift.detections.total{stack, env, outcome}` - counter
- `reeve.drift.duration{stack, env}` - histogram
- `reeve.drift.stacks_in_drift{env}` - gauge
- `reeve.drift.ongoing_duration{stack}` - gauge
- `reeve.drift.runs.total{outcome}` - counter
