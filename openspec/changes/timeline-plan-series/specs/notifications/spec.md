# Notifications delta

## MODIFIED Requirements

### Requirement: timeline_github groups entries into plan series

`timeline_github` MUST group entries into series. A series MUST begin at a plan
event, and the plan event MUST be the series' first entry.

Each series MUST occupy its own PR comment. Delivering the first entry of a
series MUST create a new comment; later entries in that series MUST edit that
comment in place. A previous series' comment MUST NOT be edited or deleted once
its successor opens.

A new series MUST be minted when the commit SHA has no series, and when a plan
is explicitly requested for a SHA that already has one. A plan event that is
not an explicit request and whose SHA already has a series MUST append to that
series, so a retried CI run does not split one plan across two comments.

An event other than a plan MUST append to the SHA's most recent series, opening
series 1 if the SHA has none.

When plan runs overlap on the same SHA, each plan's finish event MUST append
to the series opened by that plan's start event.

The marker for the first series of a SHA MUST remain
`<!-- reeve:timeline:v1:{shortsha} -->`. Later series MUST carry a distinct
marker derived from the SHA and the series ordinal.

Persisted timeline state MUST remain backward-readable: entries written before
series grouping MUST load as that SHA's first series.

#### Scenario: Plan requested again on the same commit

- **WHEN** a plan is explicitly requested for a SHA that already has a series
- **THEN** a new series opens with the plan as its first entry
- **AND** the new series is delivered as a new PR comment
- **AND** the previous series' comment is left as written

#### Scenario: CI retries the same plan

- **WHEN** a planning event arrives for a SHA whose current series is not an explicit new request
- **THEN** the entry appends to that series
- **AND** no new comment is created

#### Scenario: Explicit plans overlap

- **WHEN** a second explicit plan starts before the first explicit plan finishes
- **THEN** each finish event appends to the series opened by its own start event
- **AND** neither series contains entries from the other plan run

#### Scenario: Apply follows a plan

- **WHEN** apply lifecycle events arrive for a SHA with an open series
- **THEN** they append to that series' comment

#### Scenario: State predates series grouping

- **WHEN** timeline state written before this change is loaded
- **THEN** its per-SHA entries load as series 1
- **AND** that series keeps the pre-change marker

### Requirement: timeline_slack threading is unaffected by series

`timeline_slack` MUST continue posting every entry as a thread reply under the
one PR-level anchor message. A new plan series MUST NOT create a new anchor or
a new thread.

#### Scenario: New series while Slack timeline is enabled

- **WHEN** a new plan series opens
- **THEN** the Slack timeline appends to the existing PR thread
