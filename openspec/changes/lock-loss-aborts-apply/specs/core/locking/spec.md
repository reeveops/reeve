# Locking - deltas

## ADDED Requirements

### Requirement: Refresh-only lease transitions

The lease heartbeat MUST use a refresh-only transition that extends the lease
only while the lock's current holder still identifies the caller (same PR and
run ID). The refresh MUST NOT evict a foreign holder, install a holder, adopt
a promoted reservation, or enqueue. A lease whose own expiry has lapsed MUST
still refresh while the stored holder identifies the caller and the
conditional write succeeds over live state.

When a refresh finds the caller no longer holding the lock, the caller MUST
be told the observed lock state (released, or the PR that now holds it) and
the operation MUST abort instead of continuing to run unheld. The engine is
interrupted through its existing cancellation and grace path, and the lock
MUST NOT be re-taken by the aborted operation.

#### Scenario: Force-unlock mid-apply

- **GIVEN** an apply holding a stack lease with a live heartbeat
- **WHEN** the lock is force-unlocked mid-apply
- **THEN** the heartbeat reports the loss naming the observed state
- **AND** the engine context is cancelled and the stack fails with an abort
  message that states the lock was lost, when, the observed state, that
  resources may be partially applied, and that `/reeve preview` shows the
  current state
- **AND** the aborted operation leaves the lock without a holder and does
  not re-acquire it

#### Scenario: A late heartbeat tick after a scheduling stall

- **GIVEN** an apply whose heartbeat tick arrives after the lease expiry
  because of scheduling delay
- **WHEN** the stored holder still identifies the same PR and run and the
  conditional write succeeds
- **THEN** the lease is extended from the current time and the apply
  continues
