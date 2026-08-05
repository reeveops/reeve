# Notifications — retroactive sync delta

## ADDED Requirements

### Requirement: Channels can group drift alerts per delivery

A channel MAY set `grouping:` to batch one run's drift alerts:
`none` (default; one message per drifted stack — unset behaves the same)
or `by_environment` (one message per environment, listing that
environment's drifted stacks). Grouping SHALL be a delivery-layer concern
only — it never changes classification, state, `exit_on`, or which events
fire. It applies to the drift alert lifecycle (`drift_detected`,
`drift_ongoing`, `drift_resolved`); `check_failed` SHALL never be grouped
(each is a distinct per-stack incident). Grouping is meaningful for
`slack` and `webhook`; it is a no-op for channels where per-stack tracking
is the point (`github_issue`, where an issue is a per-stack incident, and
`otel_annotation`). An unknown `grouping:` value SHALL be a hard config
error.

#### Scenario: Mass drift is one page, not fifty

- **WHEN** a run detects drift on 50 stacks across two environments and the
  slack channel sets `grouping: by_environment`
- **THEN** the channel posts two messages, one per environment, each listing
  its drifted stacks

#### Scenario: Check failures stay per-stack

- **WHEN** the same run also has three failed checks
- **THEN** three separate `check_failed` messages are delivered regardless
  of grouping
