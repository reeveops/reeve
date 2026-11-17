# Locking - deltas

## ADDED Requirements

### Requirement: Forced releases are audited

Every forced lock release MUST write a write-once audit record naming the
actor, the stack, the authorization scope (admin override or PR-scoped
`--force`), the previous holder including whether its lease was still active,
and the promoted queue entry if any. The intent record MUST be written before
the mutation: when the audit write fails for ANY reason, including a write-once
precondition failure, the force-unlock MUST be refused and no lock state may
change. Every successful forced single-stack removal MUST write a completion,
including when the PR existed only in the queue and there is no previous holder
snapshot. Text reaching the record MUST pass redaction first.

#### Scenario: Admin force-unlock of an active apply

- **GIVEN** PR 7 holds a stack lease and an authorized actor runs
  `reeve locks unlock <stack> --force`
- **WHEN** the override clears the holder and promotes the queue
- **THEN** an audit record pair exists naming the actor, the stack, the
  previous holder (PR 7, its run and actor, lease active), the promoted PR,
  and the redacted reason
- **AND** the intent entry was written before the holder was cleared

#### Scenario: Audit store refuses the record

- **GIVEN** an admin force-unlock attempt
- **WHEN** the intent audit record cannot be written
- **THEN** the force-unlock is refused with the audit-write error
- **AND** the lock state is unchanged

#### Scenario: PR-scoped force removes a queue-only entry

- **GIVEN** PR 8 is present only in a stack's queue and there is no current
  holder
- **WHEN** an actor runs `reeve locks unlock <stack> --pr 8 --force`
- **THEN** PR 8's queue entry is removed and the lock remains free
- **AND** the audit intent is paired with a successful completion even though
  the override has no previous holder snapshot
