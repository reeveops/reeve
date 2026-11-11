# Notifications - deltas

## MODIFIED Requirements

### Requirement: Start notifications follow their lifecycle points

The `approved` (or `break_glass`) and `applying` notifications MUST fire
only after per-stack gate evaluation has passed for at least one stack, and
MUST NOT fire for a run whose gates deny every stack. They fire at most
once per run, before credential or engine work for the first proceeding
stack.

#### Scenario: A gate-blocked apply announces nothing

- **GIVEN** a notification channel subscribed to `approved` and `applying`
- **WHEN** the apply's gates deny every stack
- **THEN** neither `approved` nor `applying` is sent
- **AND** the gate comment naming the gate is the only notice

#### Scenario: Gates pass for a stack

- **GIVEN** the same subscription
- **WHEN** the first stack's gates pass
- **THEN** `approved` (or `break_glass` for an authorized override) and
  `applying` fire once before its credentials and engine run
