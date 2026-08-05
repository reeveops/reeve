# Observability

## Principle

Opt-in. Off by default. reeve emits telemetry only when
`observability.yaml` exists. reeve never hosts or sees telemetry data -
it goes to whatever endpoint the user configured.

## Signal model

- One trace per run. `preview` and `apply` are separate runs, separate
  traces. PR number and commit SHA are span attributes on the root span.
- Per-stack spans within a run. Operations (preview, policy-eval,
  lock-acquire, comment-render) are children.
- Metrics labeled by project, stack, env, op.
- Annotations via secondary emitters (Grafana, Datadog, Dash0, webhook).

## Metrics (examples)

- `reeve.runs.total` (counter; op, outcome)
- `reeve.stack.duration` (histogram; project, stack, env, op)
- `reeve.lock.wait_duration` (histogram)
- `reeve.lock.queue_depth` (gauge)
- `reeve.policy.violations` (counter; policy_name)
- `reeve.preconditions.failed` (counter; gate)
- `reeve.approvals.time_to_approval` (histogram)
- `reeve.stack.changes` (counter; type)

## Stack-name cardinality

Stack names on a large monorepo blow up OTEL cardinality. Config flag:

```yaml
observability:
  otel:
    stack_cardinality: hash   # allow | hash | drop
```

- `hash` (default): emit a stable 64-bit fingerprint of `{project}/{stack}`
  as the label. Dashboards can group without cardinality explosion.
- `allow`: raw stack names (opt-in for small deployments).
- `drop`: no stack label at all.

## Redaction

What goes in telemetry:
- Counts, durations, timestamps.
- Stack / project / env names (per cardinality rule).
- PR number, commit SHA, run ID.
- Outcome, gate names (failed preconditions), policy names.

What **never** goes in telemetry:
- Plan output, resource diffs.
- Resource names or values.
- Approver identity beyond counts.
- Any value Pulumi marked `[secret]`.

Enforced via the `internal/core/redact` pipeline - same pass that gates
PR comments and audit logs.

## Configuration

Follows OTEL conventions. Standard env vars
(`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS`) work out of
the box; config file overrides.

## Annotation emitters

Thin HTTP-POST layer for Grafana, Datadog, Dash0, generic webhook.
Per-channel event-type filter. Complements OTEL; doesn't replace it.
