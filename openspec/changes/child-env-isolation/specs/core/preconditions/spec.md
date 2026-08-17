# Preconditions - deltas

## MODIFIED Requirements

### Requirement: Evaluation

Policy is the final apply gate. Reeve evaluates fork, draft, base, checks,
preview, approval, lock, and freeze gates before starting policy hooks.

#### Scenario: Independent gate fails

- **GIVEN** a non-policy apply gate fails
- **WHEN** reeve evaluates the stack
- **THEN** evaluation stops without executing repository-controlled policy code
- **AND** the lock is released when reeve acquired it for this request
