# Core - deltas

## ADDED Requirements

### Requirement: Composite action input boundary

The composite action MUST pass caller-controlled values through environment or
action metadata fields. It MUST NOT embed GitHub expressions in shell source.

#### Scenario: Shell syntax in an input

- **GIVEN** an action input contains command substitution or shell separators
- **WHEN** the composite action dispatches reeve
- **THEN** the text is passed only as argument data
- **AND** no injected command executes

### Requirement: Immutable action dependencies

Every external action in the composite action and repository workflows MUST use
a full commit SHA.

#### Scenario: Workflow dependency update

- **WHEN** a contributor changes an external `uses:` reference
- **THEN** repository tests reject any ref that is not a 40-character SHA

### Requirement: Prebuilt binary authentication

A downloaded reeve binary MUST have a matching checksum whose keyless cosign
bundle verifies against the source repository's GitHub Actions identity.

#### Scenario: Cosign unavailable

- **GIVEN** cosign is unavailable or the release bundle is missing
- **WHEN** the action attempts the binary fast-path
- **THEN** the download is rejected
- **AND** the action builds reeve from source
