# Locking - deltas

## ADDED Requirements

### Requirement: Gate-denied runs leave the queue

A queued apply that fails a gate other than `lock_acquirable` MUST remove its
lock queue entry for that stack, and the gate-blocked comment MUST state the
removal and the re-apply step. A run blocked ONLY by `lock_acquirable` keeps
its deliberate queue entry. A failed queue removal MUST be reported instead
of silently swallowed.

#### Scenario: Queued PR fails approvals while waiting

- **GIVEN** PR A's apply queued for a stack whose lock is held by PR B
- **WHEN** A's gate evaluation fails on a gate other than the lock
- **THEN** A's queue entry is removed and the PR is not promoted when B
  releases
- **AND** the gate-blocked comment says the PR was removed from the lock
  queue and that re-running `/reeve apply` will re-queue it
- **AND** the next legitimately queued PR is promoted when B releases

#### Scenario: Queued PR blocked only by the lock

- **GIVEN** PR A's apply queued for a held lock with all other gates green
- **WHEN** A's gate evaluation blocks on `lock_acquirable`
- **THEN** A's queue entry stays and no removal note is emitted
