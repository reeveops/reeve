# approved-notification-after-gates

## Why

`approved` and `applying` notifications were emitted before any per-stack
gate evaluation. A run whose gates denied everything (missing approval,
blocked lock, missing preview) announced "approved" and "applying" to
notification channels while nothing applied - a false signal exactly where
operators watch for one.

Break-glass has a different lifecycle: its notification reports that an
authorized emergency override is in flight, not that any stack passed a gate.
It must therefore remain loud even when a non-overridable gate subsequently
blocks every stack.

## What

Apply evaluates every target stack's gates before starting engine work. For a
normal apply, `approved` and `applying` then fire once and list only the
gate-passing stacks. A fully denied normal apply sends neither; its gate
comment names the refusal.

For an authorized break-glass apply, `break_glass` fires at authorization,
before gate evaluation and regardless of the final gate outcome. It never
sends `approved`, and sends `applying` only if at least one stack passes.

Every gate-passing stack's lock is acquired during the gate phase, before the
first engine call. Its heartbeat starts immediately after acquisition and
runs until that stack completes or is refused, so an earlier long apply
cannot expire a later stack's lease.

## Scope

- `internal/run/apply.go`: separate gate and apply phases, preserve
  authorization-time break-glass notification, and protect acquired locks
  through their applies.
- Apply tests cover fully blocked normal and break-glass runs, partial passes,
  and a later stack whose lock outlives its original lease during an earlier
  apply.
- The notifications delta records the distinct normal and break-glass event
  timing.

## Impact

No config change. Notification timing and lock lease coverage change; later
notifications such as `applied` are unaffected.
