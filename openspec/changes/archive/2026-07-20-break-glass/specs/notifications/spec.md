# Notifications — break-glass delta

## ADDED Requirements

### Requirement: The reserved break_glass event gains its producer

The apply pipeline SHALL emit the `break_glass` event for an authorized
break-glass run — after authorization succeeds and before stacks are
applied — in place of the `approved` event (approvals were bypassed, not
granted). The payload SHALL carry the standard PR-flow context (PR, commit
SHA, that run's CI URL, target stacks). Channels subscribed to `break_glass`
(the deployment timeline by default; anything else explicitly) SHALL fire;
legacy default subscriptions remain unwidened.

#### Scenario: Timeline records the override

- **WHEN** an authorized break-glass apply runs with a timeline channel
  configured
- **THEN** a break-glass timeline entry is recorded for that commit SHA,
  linked to the apply run

#### Scenario: Non-break-glass applies are unchanged

- **WHEN** a normal apply runs
- **THEN** no `break_glass` event is emitted and the `approved`/`applying`
  sequence is exactly as before

### Requirement: The break-glass apply comment is loud

The apply PR comment for a break-glass run SHALL contain a distinct
marker-tagged section (`<!-- reeve:break-glass:v1 -->`) rendered as a
warning admonition, showing the actor, the matched authorization source,
the overridden gates, the same-PR-authorizing-config-modified flag when
set, and the justification quoted verbatim. An audit record SHALL be
written via the write-once audit log carrying the same fields plus the
commit SHA, run URL, and timestamps.

#### Scenario: Marker and justification present

- **WHEN** a break-glass apply completes
- **THEN** the apply comment contains the break-glass marker, a warning
  admonition, and the quoted justification

#### Scenario: Malformed command posts help, runs nothing

- **WHEN** a `/reeve breakglass` comment fails the strict parse (missing
  quotes, empty justification, wrong verb)
- **THEN** a helpful error comment with the correct usage is posted and no
  run starts
