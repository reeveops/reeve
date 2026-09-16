# Blob Storage

## Responsibility

Locks, run artifacts, drift state, audit logs. The user owns the bucket -
reeve never sees the data after delivery.

## Adapters (v1)

S3, GCS, Azure Blob, R2, filesystem. Filesystem is also the test harness
for all core components.

## Layout

```
<bucket>/reeve/
├── locks/
│   └── {project}/{stack}.json
├── runs/
│   └── pr-{number}/
│       ├── {run-id}/
│       │   ├── manifest.json
│       │   ├── {project}-{stack}/
│       │   │   ├── preview.json
│       │   │   ├── plan.bin
│       │   │   ├── summary.json
│       │   │   └── stdout.log
│       │   └── latest -> {run-id}
│       └── applied/{sha}.json       # written after a clean apply
├── drift/
│   ├── runs/{run-id}/
│   │   ├── manifest.json
│   │   ├── results/{project}-{stack}.json
│   │   └── report.md
│   ├── state/{project}/{stack}.json
│   └── suppressions/{project}/{stack}.json
├── notifications/pr-{number}/slack.json
└── audit/{year}/{month}/{day}/{run-id}.json
```

## Conditional writes

Run IDs for CI invocations include the provider's run number, commit identity,
and attempt when available. This keeps reruns in separate artifact and audit
prefixes.

All adapters must implement atomic conditional writes (If-Match on ETag,
GCS generation preconditions, filesystem flock+rename). `ErrPreconditionFailed`
signals "someone else got there first" - lock state machine re-reads.

## Executable provider contract

Every adapter MUST pass the shared `internal/blob/blobtest` contract against
an isolated namespace before it is enabled in a maintained workflow lane.

The contract covers missing-object normalization, put/get/overwrite, recursive
listing, idempotent deletion, conditional create/update, and concurrent create.

#### Scenario: A stale writer loses

- **WHEN** a writer replaces an object using its current ETag
- **THEN** a second write using the prior ETag returns `ErrPreconditionFailed`
- **AND** the second write does not change the stored object

#### Scenario: One concurrent creator wins

- **WHEN** concurrent writers conditionally create the same absent key
- **THEN** exactly one write succeeds
- **AND** every losing write returns `ErrPreconditionFailed`

## Retention

- `runs/` artifacts: pruned by `reeve maintenance run`, age-based. Default `720h` (1 month) via `retention.max_age`; `0`/negative disables.
- Locks: reaped on TTL expiry, not by retention.
- Age-based only - PR-close/merge cleanup needs VCS wiring reeve does not have.

## Failure modes

Each adapter's implementation specifies: behavior on mid-operation bucket
unavailability, recovery procedure, and manual intervention steps.
