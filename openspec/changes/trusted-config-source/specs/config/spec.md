# Config delta

## ADDED Requirements

### Requirement: PR configuration has explicit source ownership

PR operations MUST build effective configuration from `trusted_config_revision` and the checked-out PR HEAD.
Every field MUST be trusted-owned unless the specification explicitly marks it workload-owned.

#### Scenario: A PR modifies approval policy

- **WHEN** a PR changes approvals or preconditions in `.reeve/shared.yaml`
- **THEN** the current run uses the value from `trusted_config_revision`
- **AND** the preview reports that the proposed control change is not active

#### Scenario: A PR adds a stack

- **WHEN** the base revision contains a valid engine policy container
- **AND** a PR adds a uniquely keyed stack declaration using only workload-owned fields
- **AND** every trusted-owned stack descendant has a base-resolved template
- **THEN** preview uses the head-owned declaration with trusted auth, state, binary, execution, and policy-hook settings

#### Scenario: A PR changes an existing stack

- **WHEN** HEAD contains the same normalized stack identity as the base
- **THEN** merge copies only workload-owned descendants from HEAD
- **AND** every trusted-owned descendant remains sourced from the matching base entry

#### Scenario: A new stack lacks a trusted template

- **WHEN** HEAD adds a stack identity with a trusted-owned descendant that has no base-resolved template
- **THEN** validation rejects the HEAD configuration before merge or side effects

#### Scenario: A PR adds a stack with trusted controls

- **WHEN** a PR-added stack object contains authentication, state, binary, execution, or policy-hook fields
- **THEN** strict validation rejects the HEAD configuration before merge or side effects

#### Scenario: Trusted configuration is unavailable

- **WHEN** the exact base revision or any required trusted config file cannot be read and validated
- **THEN** the PR operation fails before credentials, network sinks, policy hooks, or engine execution

#### Scenario: A field has no ownership rule

- **WHEN** a new config field is not explicitly classified as workload-owned
- **THEN** the effective configuration uses only the trusted value

### Requirement: Trusted environment expansion occurs after ownership

Controller environment references MUST expand only in trusted-owned fields loaded from `trusted_config_revision`.
HEAD values MUST NOT introduce an environment reference at any path.

#### Scenario: A PR adds an environment reference

- **WHEN** the PR HEAD adds `${env:CONTROLLER_SECRET}` to any config field
- **THEN** validation fails before merge, credential handling, network access, or execution
- **AND** the error does not include the variable name or reveal whether it exists

#### Scenario: Trusted configuration already contains an environment reference

- **WHEN** HEAD preserves the identical raw scalar at the same path as the base revision
- **THEN** only the trusted base scalar is eligible for expansion
- **AND** no component interprets the scalar read from HEAD
