# Locking

## Granularity

Per-stack. Acquired at apply, not preview - previews are parallel-safe.

## Storage

Single JSON object per stack: `locks/{project}/{stack}.json`. Atomic
transitions via conditional writes:
- S3: `If-Match` on ETag
- GCS: generation preconditions
- Azure Blob: `If-Match` on ETag
- Filesystem: `flock` + fsync + rename

## Holder identity

PR + RunID. Re-acquire by the same PR **and** same run refreshes the
lease (idempotent). A different run of the same PR is refused while the
holder's lease is unexpired - two runs of one PR never apply
concurrently, and the same PR never queues behind itself. Release
requires both PR and RunID to match the holder; a non-holder release
falls back to queue removal.

## Queue

FIFO. Queue entries visible in PR comment and via `reeve locks list`.
"Blocked by PR #X" surfaces in a comment on the waiting PR (naming the
holder PR).

## TTL & Reaper

Default TTL: 4h. Configurable per `shared.yaml` `locking.ttl`. The
configured TTL also bounds the lease granted to a holder promoted from
the queue.

**Reaper is opportunistic** - there is no daemon. Every `reeve` invocation
scans `locks/` for expired TTLs before acquiring. Quiet repos may run an
optional scheduled GH Actions workflow (`reeve locks reap`) to sweep.
No control plane.

## Release triggers

- Apply finished → the finishing run releases per stack, then removes its PR from
  every lock its PR still appears in (holder or queue) so the PR does
  not linger in queues for stacks it no longer needs.
- PR merged / closed unmerged → `/reeve unlock [project/stack]` PR comment
  or `reeve locks unlock [project/stack] --pr N` removes the PR from
  holder/queue (all locks when the stack is omitted). PR-scoped removal
  only touches that PR's own entries and is not admin-gated.
- TTL expiry (opportunistic reaper).
- Manual force-unlock: `reeve locks unlock [project/stack]` (no `--pr`)
  clears holders regardless of PR - gated by `shared.yaml`
  `locking.admin_override`.

## Fairness

FIFO with TTL provides bounded wait. Load-test fixture (Phase 2) verifies
no starvation under churn.

## Failure modes

- Mid-apply blob unavailability: apply fails, lock state is indeterminate.
  Next invocation detects via TTL or explicit admin override.
- Clock skew: adapters prefer server-side timestamps (S3 `LastModified`,
  GCS `updated`) over local clock. The filesystem adapter relies on the local
  clock, so lock TTLs there are only as accurate as the host clock.
