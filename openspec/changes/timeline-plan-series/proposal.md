# Timeline plan series

## Why

`timeline_github` keys one comment per commit SHA and edits it in place. A PR
that is planned repeatedly against the same commit therefore accumulates
lifecycle entries from unrelated plan attempts in a single comment, and the
reviewer cannot tell where the current attempt begins. On a PR with several
plan comments already present, the edited-in-place comment is also not
obviously the live one.

The timeline is an append-only activity log, so the fix is not to reset it. A
new plan starts a NEW series, and the previous series stays as written.

## What

- Group timeline entries into series. A series begins at a plan.
- `timeline_github` posts a NEW comment per series rather than editing the
  previous series' comment.
- `timeline_slack` is unchanged: entries continue as thread replies under the
  one PR-level anchor.
- A new series is minted for a new commit SHA, and for an explicitly requested
  plan on a SHA that already has one. A retried CI job on the same commit
  appends to the current series rather than opening one.
- The first entry of every series is the plan event.

## Scope

Series identity and GitHub comment lifecycle for the timeline channels. No
change to which events the timeline subscribes to, to entry content, or to the
dashboard comment markers.

## Compatibility

The first series for a SHA keeps the existing marker
`<!-- reeve:timeline:v1:{shortsha} -->` byte-identical, so comments already
live on open PRs continue to be edited in place. Subsequent series carry an
ordinal suffix. Persisted state gains series grouping and stays
backward-readable: existing per-SHA entry lists load as series 1.
