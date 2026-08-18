# Tasks

## Design gate

- [ ] Approve series identity, marker compatibility, and the mint conditions.

## Implementation

- [x] Add series grouping to the `timeline_github` persisted state, reading
      existing per-SHA entry lists as series 1.
- [x] Mint a series on an explicitly requested plan, or a SHA with no series.
- [x] Append non-plan events to the SHA's highest series.
- [x] Render the series marker: unchanged for series 1, ordinal suffix after.
- [x] Carry the explicit-plan signal through `PRPayload` and `PRNotifyInput`.
- [x] Add the preview input and the CLI flag that sets it.
- [x] Leave `timeline_slack` threading unchanged.

## Verification

- [x] Test: explicit plan on the same SHA opens a second comment; the first is
      not edited.
- [x] Test: retried planning on the same SHA appends to the current series.
- [x] Test: series 1 marker is byte-identical to the pre-change marker.
- [x] Test: state written before this change loads as series 1.
- [x] Test: concurrent series mints do not collide under CAS.
- [x] No render goldens cover the timeline; none to regenerate.
- [x] `mise run check` green.
