# PR flow delta

## ADDED Requirements

### Requirement: Preview and apply bind to the same trusted configuration

`trusted_config_revision` MUST equal the immutable target-repository base commit SHA used to load trusted configuration.
Preview artifacts MUST record this identifier.

Apply MUST snapshot PR metadata again and compare its BaseSHA with the recorded `trusted_config_revision`.
A mismatch MUST fail the preview consistency gate.

#### Scenario: Policy changes after preview

- **WHEN** BaseSHA differs from the preview's `trusted_config_revision`
- **THEN** apply fails the preview consistency gate
- **AND** a new preview under the current trusted configuration is required
