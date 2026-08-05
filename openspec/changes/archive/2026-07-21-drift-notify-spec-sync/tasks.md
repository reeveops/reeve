# Tasks

- [x] Write retroactive drift deltas (timeout_per_stack, retry budget,
      renotify_after, exit_on, classification, permanent_suppressions,
      check_recovered, durable dispatch)
- [x] Write retroactive notifications delta (channel grouping)
- [x] Write retroactive iac delta (structured per-resource drift diffs)
- [x] Write retroactive vcs delta (rate-limit transport, GHES base URL)
- [x] Fold retroactive deltas into `specs/drift`, `specs/notifications`,
      `specs/iac`, `specs/vcs`
- [x] Fold missed 2026-07-20 archived deltas: notification-channels,
      deployment-timeline, break-glass, engine-providers into
      `specs/notifications`, `specs/core/approvals`,
      `specs/core/preconditions`, `specs/iac`
