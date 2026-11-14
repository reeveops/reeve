# Locking - deltas

## ADDED Requirements

### Requirement: Forced releases are audited

Every forced lock release MUST write a write-once audit record naming the
actor, the stack, the authorization scope (admin override or PR-scoped
`--force`), the previous holder including whether its lease was still active,
and the promoted queue entry if any. The intent record MUST be written before
the mutation: when the audit write fails, the force-unlock MUST be refused and
no lock state may change. Text reaching the record MUST pass redaction first.

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
