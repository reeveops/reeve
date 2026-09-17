# Blob Storage

## Responsibility

Locks, run artifacts, drift state, audit logs. The user owns the bucket -
Reeve writes and reads this state during later invocations; no Reeve-hosted service receives it.

## Adapters (v1)

S3, GCS, Azure Blob, R2, filesystem. Filesystem is also the test harness
for all core components.

## Layout

```text
<configured bucket prefix>/
├── locks/{project}/{stack}.json
├── runs/
│   ├── pr-{number}/{run-id}/manifest.json
│   ├── pr-{number}/{run-id}/plans/{encoded-stack-ref}.plan
│   ├── pr-{number}/applied/{sha}.json
│   └── local/{run-id}/...
├── drift/
│   ├── runs/{run-id}/manifest.json
│   ├── runs/{run-id}/results/{project}-{stack}.json
│   ├── runs/{run-id}/report.md
│   ├── state/{project}/{stack}.json
│   ├── suppressions/{project}/{stack}.json
│   └── pending-events/...
├── notifications/pr-{number}/...
└── audit/{year}/{month}/{day}/{run-id}.json
```

The manifest contains stack summaries; saved plans are opaque engine artifacts and may contain sensitive values.
Storage controls and retention protect those artifacts; they cannot be redacted and remain executable.


## Conditional writes

Run IDs for CI invocations include the provider's run number, commit identity,
and attempt when available. This keeps reruns in separate artifact and audit
prefixes.

New run IDs use the full commit SHA. Legacy short-SHA run IDs remain readable.

Apply and refresh lock-holder IDs include the attempt. A retry cannot adopt an
unexpired lease from an earlier attempt, and can acquire it after expiry.

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
