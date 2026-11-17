# Preconditions delta

## MODIFIED Requirements

### Requirement: Fork PR gate

Fork PR apply MUST be denied unconditionally in the release candidate. A repository opt-in, `--force`, or break-glass MUST NOT enable it. A denied apply MUST be reported in the PR comment before credentials, locks, policy hooks, or engine execution.

Preview credentials remain separately constrained by fork preview authorization: an executing approved fork requires a matching `trust: approved_fork` binding with validated read-only proof and an isolated worker. The RC keeps all fork previews validation-only regardless of those future prerequisites.

#### Scenario: Repository opts in to fork apply

- **GIVEN** the repository explicitly opted in to fork apply under the previous policy
- **WHEN** a fork PR invokes apply in the RC
- **THEN** the fork gate denies apply before credentials, locks, policy hooks, or engine execution
- **AND** the opt-in does not enable fork mutation

#### Scenario: Repository does not opt in

- **WHEN** a fork PR invokes apply without a repository opt-in
- **THEN** the fork gate denies apply and reports the denial in the PR comment

#### Scenario: Force or break-glass cannot bypass the fork gate

- **WHEN** a fork PR invokes apply with `--force` or through break-glass
- **THEN** the fork gate still denies the operation
- **AND** the denial is reported in the PR comment

#### Scenario: Internal PR apply is unaffected

- **WHEN** a non-fork PR invokes apply
- **THEN** this gate does not deny the operation based on fork status
- **AND** the remaining independent apply gates still apply
