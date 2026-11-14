# dequeue-on-gate-block

## Why

When a PR's apply queued for a held lock and then failed a gate other than
the lock itself, its queue entry stayed behind. When the holder released, the
ghost reservation was promoted: the lock went to a run that can never pass
its gates, blocking the FIFO for PRs that legitimately follow.

## What

A gate-blocked apply that is NOT holding the lock and had queued during the
lock attempt removes its queue entry and says so on the gate-blocked comment:

> Removed from the lock queue for `\<stack\>`; re-run `/reeve apply` once the
> gates pass.

A PR blocked ONLY by the lock gate keeps its queue entry on purpose: waiting
for the holder is what the queue is for. A failed queue removal is reported
loudly instead of being swallowed (the entry stays and the comment says so).

## Scope

- `internal/run/apply.go` gate-block branch only.

## Impact

- No config change.
- Minor behavior change: the blocked PR re-queues on its next `/reeve apply`.
