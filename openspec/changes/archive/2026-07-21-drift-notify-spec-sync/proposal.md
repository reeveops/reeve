# Drift/notify spec sync (retroactive)

## Why

The `next` stabilization line shipped several behavior changes without spec
deltas, and the 2026-07-20 archival (`spec: archive shipped openspec changes`)
moved four proposals into `changes/archive/` without folding their delta specs
into `specs/`. Both left `openspec/specs/` out of sync with the code — in one
case (`retry_on_transient_error`) actively contradicting it.

This change is retroactive: the features are already implemented, tested, and
documented in `docs/`. It records the deltas that should have accompanied them
and folds everything into `specs/`, restoring `specs/` as source of truth.

## What

Retroactive deltas for shipped-but-unspec'd behavior:

- **drift**: `behavior.timeout_per_stack`; `retry_on_transient_error` as an
  integer retry budget (spec previously said "retry once");
  `behavior.renotify_after` flap damping; `behavior.exit_on` exit-code
  control; `classification:` drift-noise filtering; declarative
  `permanent_suppressions:`; the `check_recovered` event; durable
  at-least-once notification dispatch via pending-event markers.
- **notifications**: channel-level `grouping:` batching for drift alerts.
- **iac**: structured per-resource drift diffs (`PreviewResult.Resources`,
  `ResourceChange`).
- **vcs**: rate-limit-aware GitHub transport; GHES support via
  `GITHUB_API_URL`.

Completing the missed fold (no new deltas needed — the archived deltas under
`changes/archive/2026-07-20-*/specs/` are applied as written):

- `notification-channels`, `deployment-timeline`, `break-glass` →
  `specs/notifications/`, `specs/core/approvals/`, `specs/core/preconditions/`.
- `engine-providers` → `specs/iac/`.

## Scope

Documentation only. No code changes; every requirement below describes
behavior already shipped on `next` (PRs #46, #47, #50, #52 and the archived
2026-07-20 changes).
