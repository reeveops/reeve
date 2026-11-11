# Preconditions - deltas

## MODIFIED Requirements

### Requirement: Required-checks gate

A commit with no check runs AND no combined statuses MUST NOT pass the
required-checks gate: zero observed checks is not passing evidence. The
denial MUST name the SHA, the rule, and the opt-out key
(`preconditions.allow_zero_checks`). The gate MAY treat zero evidence as
passing only when the operator opts out via that key or the matching CLI
flag - repositories where reeve is the only check. Legacy combined statuses
count as check evidence; zero check runs with statuses present follows the
normal status path. The failing-check detail MUST surface in the gate reason
on the PR comment.

#### Scenario: No checks ran at all

- **GIVEN** `require_checks_passing: true` and a commit with zero check runs
  and zero statuses
- **WHEN** the required-checks gate evaluates
- **THEN** the apply is denied
- **AND** the refusal reads: "Required checks: no check runs found on
  `<sha>`. Zero checks is not treated as passing. Wait for CI, or set
  `preconditions.allow_zero_checks` if this repo has no other checks."

#### Scenario: A repo whose only check is reeve

- **GIVEN** the same commit evidence and
  `preconditions.allow_zero_checks: true`
- **WHEN** the required-checks gate evaluates
- **THEN** the gate passes for lack of other checks
