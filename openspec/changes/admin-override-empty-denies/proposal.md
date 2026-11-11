# admin-override-empty-denies

## Why

The admin force-unlock gate skipped its check when
`locking.admin_override.allowed` was unset or empty: "not configured" meant
"anyone". The dangerous path - force-clearing an active holder - was
therefore decorative by default, and `actorAllowed`'s own empty-list rule
(deny) was never reached.

## What

An empty `locking.admin_override.allowed` denies everyone (D1). The refusal
names the lock, the rule, and the fix:

> force-unlock refused: \`locking.admin_override.allowed\` is empty, so no one
> may force-unlock \<project/stack\>; add the actor to that list.

A listed actor still passes. PR-scoped removal (`locks unlock --pr N`,
`/reeve unlock`) is NOT admin-gated and is unaffected: it only ever touches
that PR's own entries, the same thing a finishing apply does automatically.

## Scope

- `cmd/reeve/locks.go`: the force-unlock gate (moved after argument parsing
  so the refusal names the stack).

## Impact

- **Config change (breaking):** operators who use the CLI force-unlock must
  populate `locking.admin_override.allowed`.
- No new keys; `allow-associations`-style lists elsewhere are unchanged.
