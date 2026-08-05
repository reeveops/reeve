# Notifications — deployment timeline delta

## ADDED Requirements

### Requirement: The event model distinguishes preview started from finished

The PR-flow event set SHALL include `planning` (preview run started) in
addition to `plan` (preview finished). The run pipeline SHALL emit
`planning` at the start of a preview run. A reserved `break_glass` event
SHALL be a valid subscription with no producer yet, keeping the surface
extensible for emergency-override runs. The legacy Slack trigger-onward
default subscription SHALL NOT be widened by these additions: existing
channels' subscriptions keep their exact prior behavior.

#### Scenario: Legacy defaults unchanged

- **WHEN** a channel relies on the legacy default subscription (no `on:` list)
- **THEN** it does not receive `planning` or `break_glass` events

#### Scenario: Preview start is observable

- **WHEN** a preview run starts for a PR
- **THEN** channels subscribed to `planning` receive a payload carrying the PR,
  commit SHA, and that run's CI URL

### Requirement: Timeline channels are explicit, default-off channel types

The deployment timeline SHALL be provided by two channel types —
`timeline_slack` and `timeline_github` — enabled only by explicit entries in
the `channels:` config list. With no timeline channel configured, behavior is
byte-identical for existing users: no new messages, comments, or markers.
When `on:` is omitted, timeline channels SHALL default to every PR-flow
timeline event (planning through blocked, plus break_glass).

#### Scenario: Default off

- **WHEN** a repo upgrades reeve without touching notifications.yaml
- **THEN** no timeline comment or thread entry is produced

### Requirement: Every timeline entry carries its run context

Each timeline entry SHALL carry the event, the commit SHA (rendered short),
a timestamp, and the CI run URL of the run that produced that event —
preview and apply runs link to their own distinct runs. Events that carry
stack results (`plan`, `applied`, `failed`, `blocked`) SHALL include a
per-stack outcome summary.

#### Scenario: Preview and apply link different runs

- **WHEN** a PR's preview ran in CI run A and its apply in CI run B
- **THEN** the preview entries link run A and the apply entries link run B

### Requirement: Slack timeline threads under one PR-level anchor

`timeline_slack` SHALL post every entry as a thread reply under ONE
PR-level anchor message (no channel-level spam). The anchor SHALL be the
dashboard slack channel's per-PR status message when present (shared per-PR
blob state, conditional writes via `PutIfMatch`); otherwise the timeline
SHALL create a minimal anchor which the dashboard channel subsequently edits
into the status message. On a create race, the first writer's anchor wins
and the timeline threads under it. Once the timeline claims the thread, the
dashboard channel SHALL suppress its own courtesy thread notes so events are
not double-posted.

#### Scenario: Anchor reuse

- **WHEN** the dashboard slack channel already created the per-PR message
- **THEN** timeline entries appear as replies in that message's thread and
  no second channel message is posted

#### Scenario: No double thread entries

- **WHEN** both `slack` and `timeline_slack` deliver the same event
- **THEN** exactly one thread entry (the timeline's) is posted

### Requirement: GitHub timeline comments are grouped by SHA

`timeline_github` SHALL maintain one PR comment per commit SHA, identified
by a NEW marker namespace (`<!-- reeve:timeline:v1:{shortsha} -->`) and
edited in place via the existing comment-upsert machinery. Existing comment
markers SHALL remain byte-identical. Entry history SHALL be persisted per
PR in blob state with conditional (compare-and-swap) appends so concurrent
runs cannot lose each other's entries; each event re-renders the SHA's full
comment from state.

#### Scenario: New commit, new comment

- **WHEN** a PR's preview runs for SHA A and then for newly pushed SHA B
- **THEN** two timeline comments exist, one per SHA, each accumulating only
  its own SHA's entries

#### Scenario: Concurrent runs merge

- **WHEN** two runs append timeline entries for the same PR concurrently
- **THEN** both entries survive in state and the final comment shows both

### Requirement: Timeline channels stay inside the modularity contract

`timeline_github` SHALL consume a narrow, consumer-defined comment surface
(marker upsert) implemented by the VCS adapter; no VCS SDK is imported by
the channel. Both timeline channels SHALL skip (not fail) when their runtime
dependencies are absent (no comment client / blob store / Slack token).

#### Scenario: Drift runs skip the timeline

- **WHEN** the drift runner dispatches with no PR comment client configured
- **THEN** the timeline_github channel is skipped and drift delivery proceeds
