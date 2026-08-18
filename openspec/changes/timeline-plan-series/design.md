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

Only a plan event opens a series, and only when the payload marks it a fresh
plan request. Two cases mint:

- The SHA has no series yet. Covers a new commit.
- The SHA has a series and the plan was explicitly requested.

A planning event that is neither - a retried or re-dispatched CI job for a plan
already in progress on that SHA - appends to the current series. Without this
an infrastructure retry would split one plan across two comments.

Every non-plan event appends to the SHA's highest existing series. An event
arriving for a SHA with no series (an apply on a commit whose plan predates
this change) opens series 1 so no entry is dropped.

## Signalling an explicit plan request

`PRPayload` gains a field marking the plan as explicitly requested. `run.Preview`
sets it from a new preview input, which `cmd/reeve` sets from a flag the
workflow passes when the run came from a `/reeve plan` comment.

The signal is a flag rather than reeve reading `GITHUB_EVENT_NAME` itself:
core-adjacent code must not branch on a VCS provider's event names, and a flag
is exercisable in local tests, which the repository's local-first testing rule
requires.
