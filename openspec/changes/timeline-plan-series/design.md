# Design

## Series identity

A series is identified by short commit SHA plus a 1-based ordinal. The ordinal
is held in the same per-PR blob object as the entries and advanced under the
existing compare-and-swap append, so two concurrent runs cannot both claim the
same new series.

Marker: series 1 renders `<!-- reeve:timeline:v1:{shortsha} -->`, unchanged.
Series n>1 renders `<!-- reeve:timeline:v1:{shortsha}:{n} -->`. Keeping series
1 byte-identical is what lets an in-flight PR keep its existing comment.

Because each series has its own marker and no comment carries it yet, the
existing upsert path creates a new comment on the first entry of a series and
edits that comment for every later entry. No delete path is introduced: the
previous series' comment stays as the historical record.

## When a series is minted

A planning event for a SHA with no series opens series 1, whether or not the
payload marks it a fresh request. Covers a new commit. Once a SHA has a series,
only a payload marked a fresh plan request mints another.

A planning event that is neither - a retried or re-dispatched CI job for a plan
already in progress on that SHA - appends to the current series. Without this
an infrastructure retry would split one plan across two comments.

Every non-plan event appends to the SHA's highest existing series. As a legacy
or missing-plan recovery exception, an event arriving for a SHA with no series
(for example, apply on a commit whose plan predates this change) opens series 1
so no entry is dropped. A preview-finished event whose run identity matches no
existing series opens a recovery series rather than being guessed into the
newest overlapping plan.

Preview-started and preview-finished are separate deliveries. The channel
matches them by durable CI run ID, falling back to the CI run URL for legacy
entries. Overlapping explicit plans therefore finish in the series they opened
even when a newer series exists. Retrying the same explicit planning delivery
reuses that run's series instead of minting another; a different run identity
mints a new series.

Series-aware state is persisted under a versioned object key. If that object is
absent, the channel imports the pre-series object and writes the migrated state
to the new key. The legacy object is left untouched so an older binary cannot
decode and overwrite the newer series-aware history.

Migration is one-way, and mixed-version operation is not supported: once the
versioned object exists the loader stops consulting the legacy key, so an event
processed by a pre-series binary after migration lands in the legacy object and
is absent from the series-aware timeline. The loss is bounded - that binary
still renders its own comment under the unchanged series-1 marker - but the
entry does not appear in later series-aware renders.

Neither a merge nor a dual write is used, deliberately. Legacy state is a flat
per-SHA entry list carrying no series and no run identity, so merging it back
after migration would have to guess which series each entry belongs to, which
is the guessing the run-identity routing exists to remove. Dual writing would
hand a pre-series binary a v2 object it decodes without series awareness and
writes back truncated, which is the hazard the versioned key prevents.

Operators upgrading mid-PR should therefore roll the reeve version forward for
a repository in one step rather than running two versions against the same PR.

## Signalling an explicit plan request

`PRPayload` gains a field marking the plan as explicitly requested. `run.Preview`
sets it from a new preview input, which `cmd/reeve` sets from a flag the
workflow passes when the run came from a `/reeve plan` comment.

The Action also passes the GitHub Actions run URL, and preview carries the
provider's run ID separately as the durable request identity. Direct callers
without a provider run ID fall back to preview's stable artifact run ID so the
start and finish deliveries remain correlatable.

The signal is a flag rather than reeve reading `GITHUB_EVENT_NAME` itself:
core-adjacent code must not branch on a VCS provider's event names, and a flag
is exercisable in local tests, which the repository's local-first testing rule
requires.
