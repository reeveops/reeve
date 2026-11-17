# reject-future-preview

## Why

A preview manifest whose `created_at` is ahead of the clock passed the
freshness gate: a future stamp computes a NEGATIVE age, which holds the
freshness gate at green indefinitely. That is exactly the property a crafted
manifest (or a preview host with a broken clock) needs to keep a stale plan
applicable forever.

## What

The authoritative preview manifest for a commit is rejected when its
`created_at` is ahead of the clock by more than a small skew
(`maxPreviewClockSkew` = 2 minutes):

> preview \<run id\> rejected: created_at \<time\> is in the future (clock is
> \<now\>). Re-run /reeve preview.

The rejection fails the apply closed: no snapshot, no freshness pass. A stamp
within the skew (minor clock offset between hosts) passes and indexes
normally.

## Scope

- `internal/run/preview_lookup.go` only.

## Impact

- No config change.
- Legitimate manifests are unaffected; only future-dated stamps reject.

**Follow-ups (review):** the rejection moved from "refuse the selected
manifest" to "SKIP future-dated manifests at selection, then refuse only when
nothing valid remains" - a future stamp always sorted newest, so refusing the
winner meant the advised re-run of `/reeve preview` could never clear it. The
skip is warned with the object key and the stored stamp, the error is a typed
FuturePreviewError, selection falls to the newest VALID manifest, and the
lookup clock is injected (no package-level mutable variable).
