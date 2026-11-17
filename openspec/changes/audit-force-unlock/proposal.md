# audit-force-unlock

## Why

The admin force-unlock (`locks unlock project/stack --force`) and the
PR-scoped `--pr N --force` removal — which is what a `/reeve unlock --force`
comment dispatches — cleared holders with no durable audit record. Breaking
glass on a lock left a louder trail than breaking a lock.

## What

Every forced release writes a write-once audit record pair (intent before the
mutation, completion after) naming the actor, the stack(s), the authorization
scope (`admin` via locking.admin_override, `pr` via `--pr N --force`), the
previous holder (PR, run, actor, and whether its lease was still ACTIVE -
the difference between pruning a dead reservation and killing a live apply),
the promoted queue entry, and the reason.

The intent entry is a hard requirement in the break-glass mold: if the audit
write fails, the force-unlock is refused:

> Force-unlock refused: audit record could not be written (`\<error\>`).

and no lock was cleared. If the completion write fails after the mutation,
the unlock stands and the failure is logged loudly.

The recorded reason (and any error text) passes through `internal/core/redact`
before it reaches the audit store.

## Scope

- `cmd/reeve/locks.go`: the unlock command paths.
- `internal/audit`: the `LockOverride`/`HolderSnapshot` record types. The
  audit schema needs a home for the override record; the addition is an
  optional field, so older readers ignore it and no migration is needed.

## Impact

- No config change.
- Non-force PR-scoped cleanup (queue removal, dead reservations) is NOT
  audited: it is parity with what a finishing apply does automatically.
