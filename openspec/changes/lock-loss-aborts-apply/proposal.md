# lock-loss-aborts-apply

## Why

The lease heartbeat reused the acquire transition, so a refresh could
re-acquire: after a force-unlock (or release/reaper) cleared the holder
mid-apply, the next heartbeat tick installed the holder again and the apply
kept running on a lock it no longer owned. Two applies could also end up
running concurrently after the original lease expired. The review found that
this contradicts the locking spec's guarantee (ref: internal review
2026-11-10, "Heartbeat ownership loss does not stop the engine").

## What

A new refresh-only transition, `corelocks.RefreshLease`, extends the lease
only while the lock's CURRENT holder still identifies the caller (same PR and
run). It never evicts a foreign holder, installs a holder, adopts a promoted
reservation, or enqueues. The heartbeat uses it exclusively.

When a refresh reports the lease lost, the heartbeat reports the observed
state once and stops. The apply cancels the engine context - the existing
SIGINT + 30 s grace path - and fails the stack:

> Apply aborted on `\<stack\>`: lock lost at `\<time\>` (released; the lock
> has no holder). Engine interrupted; resources may be partially applied. Run
> `/reeve preview` to see the current state before re-applying.

Observed states are worded from the lock object: `released; the lock has no
holder`, `now held by PR #N`, `now held by another run of PR #N`. The lock is
never retaken; a non-holder release after the abort can only remove queue
entries and the release failure is logged with its reason.

Refresh runs abort the same way (the engine gets a cancellable context, the
stack fails with `Refresh aborted on ...`).

## Scope

- `internal/core/locks` (pure transition).
- `internal/blob/locks` (store transition + heartbeat wiring).
- `internal/run` apply and refresh call sites.

## Impact

- No config change.
- **Behavior change:** an apply or refresh that loses its lock stops instead
  of running on. A scheduling stall that lets the lease lapse is NOT an abort
  when the stored holder still identifies the run and the CAS write proves
  the caller wrote over live state.
- Actor attribution for a release ("released by @actor") is not observable
  from lock state alone; the message states the observed lock state instead.
