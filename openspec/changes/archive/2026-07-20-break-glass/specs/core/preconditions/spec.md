# Preconditions — break-glass delta

## ADDED Requirements

### Requirement: Break-glass overrides approvals always, freeze conditionally, and nothing else

For an authorized break-glass run, gate evaluation SHALL override the
approvals gate unconditionally and the freeze gate only when
`break_glass.override_freeze` is true (the default). An overridden gate
SHALL surface as a WARNING in the gate trace — visible, never silent — and
SHALL be reported in the evaluation result's overridden-gates list (which
feeds the audit record and PR comment). Break-glass SHALL NEVER override
the lock gate, and SHALL leave every other gate untouched: checks_green,
up_to_date, preview_succeeded, preview_fresh, policy, fork-PR, and draft-PR
all still apply. A gate that would have passed anyway SHALL NOT be reported
as overridden.

#### Scenario: Approvals overridden

- **WHEN** an authorized break-glass apply runs with zero approvals
- **THEN** the approvals gate warns "overridden by break-glass" and the
  stack is not blocked by it

#### Scenario: Freeze override is conditional

- **WHEN** a freeze window is active and `override_freeze` is true
- **THEN** the freeze gate warns as overridden and the apply proceeds
- **WHEN** a freeze window is active and `override_freeze: false`
- **THEN** the freeze gate fails and the stack stays blocked

#### Scenario: Locks are never bypassed

- **WHEN** another PR holds a stack's lock during a break-glass apply
- **THEN** the lock gate fails and nothing is applied for that stack

#### Scenario: Checks still bind

- **WHEN** required checks are red (with `require_checks_passing: true`)
  during a break-glass apply
- **THEN** the checks_green gate fails and the stack stays blocked
