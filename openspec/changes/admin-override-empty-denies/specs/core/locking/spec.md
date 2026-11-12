# Locking - deltas

## MODIFIED Requirements

### Requirement: Manual force-unlock is admin-gated

`reeve locks unlock <project/stack> --force` MUST refuse when
`locking.admin_override.allowed` is empty or unset - an empty allowlist
denies everyone; it is a list of specific accounts, not an opt-in switch.
The refusal MUST name the stack, the rule, and the corrective step, and
MUST happen before any lock state changes. A listed actor still succeeds.
PR-scoped removal (`--pr N`, including `/reeve unlock`) is not admin-gated
and is unaffected.

#### Scenario: Empty admin allowlist

- **GIVEN** a bucket whose `shared.yaml` sets no
  `locking.admin_override.allowed`
- **WHEN** an actor runs the admin force-unlock
- **THEN** the unlock is refused with the empty-allowlist refusal naming
  the stack
- **AND** the lock state is unchanged

#### Scenario: A PR releases its own lock

- **GIVEN** the same empty allowlist
- **WHEN** `reeve locks unlock <stack> --pr N` (or
  `/reeve unlock --force`) removes PR N's own entries
- **THEN** the removal succeeds without the admin list
