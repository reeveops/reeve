# Locking - deltas

## ADDED Requirements

### Requirement: Gate-denied runs leave the queue

A queued apply that fails a gate other than `lock_acquirable` MUST remove its
lock queue entry for that stack, and the gate-blocked comment MUST state the
removal and the re-apply step. A run blocked ONLY by `lock_acquirable` keeps
its deliberate queue entry. Cleanup MUST be scoped to the denied run's PR and
run id: if its queue entry was promoted while the other gates were evaluated,
that active holder MUST be released, while a different live run of the same PR
and other runs' queue entries MUST remain untouched. A failed queue removal
MUST be reported instead of silently swallowed.

#### Scenario: Queued PR fails approvals while waiting

- **GIVEN** PR A's apply queued for a stack whose lock is held by PR B
- **WHEN** A's gate evaluation fails on a gate other than the lock
- **THEN** A's queue entry is removed and the PR is not promoted when B
  releases
- **AND** the gate-blocked comment says the PR was removed from the lock
  queue and that re-running `/reeve apply` will re-queue it
- **AND** the next legitimately queued PR is promoted when B releases

#### Scenario: A denied run is promoted during gate evaluation

- **GIVEN** PR A's run queued for a held lock and another run or PR follows it
- **WHEN** the holder releases and promotes A's run before A's non-lock gate
  denial is handled
- **THEN** A's denied run releases its own promoted holder and leaves no queue
  entry
- **AND** the next legitimate PR is promoted
- **AND** cleanup never clears a different live run of PR A or that run's queue
  entries

#### Scenario: Queued PR blocked only by the lock

- **GIVEN** PR A's apply queued for a held lock with all other gates green
- **WHEN** A's gate evaluation blocks on `lock_acquirable`
- **THEN** A's queue entry stays and no removal note is emitted
