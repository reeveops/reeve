# Notifications delta

## MODIFIED Requirements

### Requirement: timeline_github groups entries into plan series

`timeline_github` MUST group entries into series. A normal series MUST begin at
a planning event, and the planning event MUST be the series' first entry. A
legacy or missing-plan recovery MAY begin with the first lifecycle event the
channel observes.

Each series MUST occupy its own PR comment. Delivering the first entry of a
series MUST create a new comment; later entries in that series MUST edit that
comment in place. A previous series' comment MUST NOT be edited or deleted once
its successor opens.

A new series MUST be minted when the commit SHA has no series, and when a plan
is explicitly requested for a SHA that already has one. Each planning delivery
MUST carry a durable request identity, such as the CI run ID. Duplicate explicit
deliveries with the same identity MUST reuse the series already opened for that
request; an explicit request with a different identity MUST mint a new series.
A planning event that is not an explicit request and whose SHA already has a
series MUST append to that series, so a retried CI run does not split one plan
across two comments.

An event other than a plan MUST append to the SHA's most recent series, opening
series 1 if the SHA has none, unless it is a preview-finished event with no
matching request identity. Such an event MUST open a recovery series.

When plan runs overlap on the same SHA, each plan's finish event MUST append to
the series opened by the start event with the same durable request identity.
A finish event with no matching identity MUST open a recovery series rather
than append to the SHA's newest series.

The marker for the first series of a SHA MUST remain
`<!-- reeve:timeline:v1:{shortsha} -->`. Later series MUST carry a distinct
marker derived from the SHA and the series ordinal.

Persisted timeline state MUST remain backward-readable: entries written before
series grouping MUST load as that SHA's first series. Series-aware state MUST
use a versioned persistence path so older binaries cannot overwrite it with a
schema that discards later series.

Migration MUST be one-way: once series-aware state exists for a PR, the channel
MUST NOT consult the pre-series object again. Running a pre-series and a
series-aware binary against the same PR is therefore unsupported, and an event
delivered by a pre-series binary after migration MAY be absent from the
series-aware timeline.

#### Scenario: Plan requested again on the same commit

- **WHEN** a plan is explicitly requested for a SHA that already has a series
- **THEN** a new series opens with the plan as its first entry
- **AND** the new series is delivered as a new PR comment
- **AND** the previous series' comment is left as written

#### Scenario: CI retries the same plan

- **WHEN** a non-explicit planning event arrives for a SHA that already has a series
- **THEN** the entry appends to that series
- **AND** no new comment is created

#### Scenario: Explicit delivery is retried

- **WHEN** an explicit planning delivery repeats with the same durable request identity
- **THEN** it reuses the series already opened for that request
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

#### Scenario: Pre-series binary delivers after migration

- **WHEN** series-aware state already exists for a PR
- **THEN** the pre-series object is not consulted again
- **AND** the series-aware object is not overwritten with pre-series state

#### Scenario: Preview finish has no matching start

- **WHEN** a preview-finished event has an unknown or missing request identity
- **AND** the SHA already has a series
- **THEN** the event opens a recovery series
- **AND** it does not modify the SHA's newest existing series

### Requirement: timeline_slack threading is unaffected by series

`timeline_slack` MUST continue posting every entry as a thread reply under the
one PR-level anchor message. A new plan series MUST NOT create a new anchor or
a new thread.

#### Scenario: New series while Slack timeline is enabled

- **WHEN** a new plan series opens
- **THEN** the Slack timeline appends to the existing PR thread
