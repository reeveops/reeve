# Auth - Pulumi passphrase delta

## ADDED Requirements

### Requirement: Selected Pulumi passphrase

When `engine.state.secrets_provider.type` is `passphrase`, Reeve MUST include
the selected passphrase in the isolated state and engine environment.

#### Scenario: Shared workflow passphrase

- **GIVEN** the engine selects the passphrase secrets provider
- **AND** the host supplies `PULUMI_CONFIG_PASSPHRASE`
- **WHEN** Reeve logs in or runs the engine
- **THEN** the child receives that value as `PULUMI_CONFIG_PASSPHRASE`

#### Scenario: Configured passphrase reference

- **GIVEN** the selected passphrase field resolves an environment reference
- **WHEN** Reeve logs in or runs the engine
- **THEN** the child receives the resolved value
- **AND** the resolved value overrides the standard host variable

#### Scenario: Different secrets provider

- **GIVEN** the host supplies `PULUMI_CONFIG_PASSPHRASE`
- **AND** the engine does not select the passphrase secrets provider
- **WHEN** Reeve constructs the child environment
- **THEN** the child does not receive the ambient passphrase

#### Scenario: Redaction

- **GIVEN** a selected passphrase reaches an engine child
- **WHEN** engine output contains that value
- **THEN** Reeve redacts it before persistence or rendering
