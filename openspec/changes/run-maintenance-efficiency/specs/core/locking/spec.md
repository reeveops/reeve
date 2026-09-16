## ADDED Requirements

### Requirement: Bounded lock maintenance operations

Lock walkers MUST reuse each successfully decoded lock and version within a transition attempt.
Reaping and PR cleanup MUST leave unchanged holder and queue state unwritten.

#### Scenario: Unchanged lock

- GIVEN a stored lock with no maintenance transition to perform
- WHEN locks are listed, reaped, or cleaned up for another PR or run
- THEN the walker MUST read that object once and perform no conditional write to it.

#### Scenario: Conflict with a heartbeat

- GIVEN an expired snapshot whose holder renews before the maintenance write
- WHEN the conditional write fails
- THEN the walker MUST reread and recompute expiry, preserving the renewed holder.

#### Scenario: Changed ownership during cleanup

- GIVEN a PR cleanup snapshot whose ownership changes before the write
- WHEN the conditional write fails
- THEN cleanup MUST reevaluate PR and run identity and preserve unrelated active holders.

#### Scenario: Content identity mismatch

- GIVEN a listed object whose embedded project or stack does not derive back to its key
- WHEN a walker encounters it
- THEN it MUST NOT modify that object or a different lock named by its contents.
