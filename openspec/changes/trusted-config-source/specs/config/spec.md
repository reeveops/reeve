# Config delta

## ADDED Requirements

### Requirement: PR configuration has explicit source ownership

PR operations MUST build effective configuration from an immutable trusted base SHA and the checked-out PR HEAD.
Every field MUST be trusted-owned unless the specification explicitly marks it workload-owned.

#### Scenario: A PR modifies approval policy

- **WHEN** a PR changes approvals or preconditions in `.reeve/shared.yaml`
- **THEN** the current run uses the value from the trusted base SHA
- **AND** the preview reports that the proposed control change is not active

#### Scenario: A PR adds a stack

- **WHEN** a PR adds a valid stack declaration without changing engine identity
- **THEN** preview uses the head-owned declaration with trusted auth and execution policy

#### Scenario: Trusted configuration is unavailable

- **WHEN** the exact base revision or any required trusted config file cannot be read and validated
- **THEN** the PR operation fails before credentials, network sinks, policy hooks, or engine execution

#### Scenario: A field has no ownership rule

- **WHEN** a new config field is not explicitly classified as workload-owned
- **THEN** the effective configuration uses only the trusted value

### Requirement: Trusted environment expansion occurs after ownership

Controller environment references MUST expand only in trusted-owned fields loaded from the base SHA.
Untrusted values MUST NOT select an environment name or receive the expanded value through diagnostics.

#### Scenario: A PR adds an environment reference

- **WHEN** the PR HEAD adds `${env:CONTROLLER_SECRET}` to any config field
- **THEN** the reference is not expanded for the current PR operation
- **AND** no diagnostic reveals whether the variable exists
