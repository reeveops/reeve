# Notifications - deltas

## MODIFIED Requirements

### Requirement: Start notifications follow their lifecycle points

For a normal apply, the `approved` and `applying` notifications MUST fire
only after per-stack gate evaluation has passed for at least one stack. They
MUST NOT fire when every stack is denied. They fire at most once per run,
after all gate decisions and before credential or engine work for the first
proceeding stack, and list only the gate-passing stacks.

For an authorized break-glass apply, the `break_glass` notification MUST fire
at authorization time, before per-stack gate evaluation, even when every
stack is subsequently denied. Such a run MUST NOT send `approved`; it sends
`applying` only when at least one stack passes its gates.

#### Scenario: A gate-blocked normal apply announces no start events

- **GIVEN** a notification channel subscribed to `approved` and `applying`
- **WHEN** the apply's gates deny every stack
- **THEN** neither `approved` nor `applying` is sent
- **AND** the gate comment naming the gate is the only notice

#### Scenario: A fully blocked break-glass apply still announces authorization

- **GIVEN** an authorized break-glass apply and a channel subscribed to
  `break_glass`, `approved`, and `applying`
- **WHEN** a gate that break-glass does not override denies every stack
- **THEN** `break_glass` is sent at authorization time
- **AND** neither `approved` nor `applying` is sent
- **AND** no stack applies

#### Scenario: Gates pass for a normal apply

- **GIVEN** a channel subscribed to `approved` and `applying`
- **WHEN** one or more stacks pass their gates
- **THEN** `approved` and `applying` each fire once before credentials and
  engine work
- **AND** their stack list contains only the gate-passing stacks

#### Scenario: Gates pass for a break-glass apply

- **GIVEN** an authorized break-glass apply
- **WHEN** one or more stacks pass their gates
- **THEN** `break_glass` has already fired at authorization time
- **AND** `approved` is not sent
- **AND** `applying` fires once for only the gate-passing stacks
