# approved-notification-after-gates

## Why

`approved` and `applying` notifications were emitted before any per-stack
gate evaluation. A run whose gates deny everything (missing approval,
blocked lock, missing preview) announced "approved" and "applying" to
notification channels while nothing applied - a false signal exactly where
operators watch for one.

## What

The `approved` / `break_glass` and `applying` notifications move below
per-stack gate evaluation: they fire once, at the first stack whose gates
pass, before credential or engine work for it. A run whose gates deny
for every stack sends neither; the gate comment is the only notice and
already names the gate.

## Scope

- `internal/run/apply.go`: the notification block moves into the per-stack
  loop after the gate decision.

## Impact

- No config change. Notification timing only; a later notification (e.g.
  `applied`) is unaffected.
