# Design

## Lock snapshots

- Read a listed lock's JSON and version together; validate that its project and stack derive back to the listed key.
- Reuse that snapshot for listing, reaping, and PR cleanup.
- A maintenance transition reports whether holder or queue membership changed; unchanged locks retain their stored timestamp and version.
- Conditional writes keep the backend CAS probe; conflicts reread and recompute the transition.
- Acquire, release, force unlock, and heartbeat continue using their existing mutation path.

## Retention follow-up

- Introduce an optional metadata-listing capability across filesystem, S3/R2, GCS, and Azure.
- Preserve unknown-age objects and protect overwritten generations before replacing the current pruning implementation.
- Add an explicit maintenance command and config migration before removing opportunistic global sweeps.

## Validation

- Count exact-key lock reads and conditional writes separately from the one-time CAS probe.
- Simulate heartbeat renewal and ownership changes between the initial read and conditional write.
- Check unchanged versions, run-scoped cleanup, FIFO promotion, corrupt identities, and rejected conditional writes.
