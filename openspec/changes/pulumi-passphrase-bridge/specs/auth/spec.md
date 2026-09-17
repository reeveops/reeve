# Auth - Pulumi passphrase delta

## ADDED Requirements

### Requirement: Selected Pulumi passphrase

When `engine.state.secrets_provider.type` is `passphrase`, Reeve MUST include
the selected passphrase in the isolated state and engine environment.

#### Scenario: State auth provider passphrase

- **GIVEN** the engine selects the passphrase secrets provider
- **AND** its state auth provider supplies `PULUMI_CONFIG_PASSPHRASE`
- **WHEN** Reeve logs in or runs the engine
- **THEN** the child receives that value as `PULUMI_CONFIG_PASSPHRASE`

#### Scenario: Configured passphrase literal

- **GIVEN** the selected passphrase field contains a configured literal
- **WHEN** Reeve logs in or runs the engine
- **THEN** the child receives the configured value
- **AND** the configured value does not read another host variable

#### Scenario: Ambient passphrase

- **GIVEN** the host supplies `PULUMI_CONFIG_PASSPHRASE`
- **AND** no selected auth provider exports that variable
- **WHEN** Reeve constructs the child environment
- **THEN** the child does not receive the host value

#### Scenario: Redaction

- **GIVEN** a selected passphrase reaches an engine child
- **WHEN** engine output contains that value
- **THEN** Reeve redacts it before persistence or rendering
