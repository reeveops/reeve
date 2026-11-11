# Preconditions - deltas

## ADDED Requirements

### Requirement: Policy plan artifacts stay in the temp dir

The policy hook plan file MUST be written under the per-run temp directory
using slugged project and stack components, so that repo-controlled names
cannot escape it through traversal or path separators. A plan file that
cannot be written MUST deny the apply - no hook runs - and the refusal MUST
name the hooks, the stack, the rule (plan file could not be written), the
error, and the outcome. Name-derived detail in the refusal passes redaction.

#### Scenario: A crafted stack name

- **GIVEN** a stack named with a `..` traversal or path separator
- **WHEN** the policy hooks' plan file is written
- **THEN** the file stays inside the per-run temp directory
- **AND** no file lands outside it

#### Scenario: The plan file cannot be written

- **GIVEN** policy hooks are configured
- **WHEN** the plan JSON cannot be written
- **THEN** the policy gate fails closed with a refusal naming the hooks,
  the stack, the rule, the underlying error, and that the apply is denied
- **AND** no hook command executed
