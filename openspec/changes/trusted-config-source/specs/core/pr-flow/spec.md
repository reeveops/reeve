# PR flow delta

## ADDED Requirements

### Requirement: Preview and apply bind to the same trusted policy

Preview artifacts MUST record the trusted base SHA used to evaluate the PR.
Apply MUST reject a preview whose trusted policy SHA differs from the current trusted base SHA.

#### Scenario: Policy changes after preview

- **WHEN** the base policy SHA changes after a successful preview
- **THEN** apply fails the preview consistency gate
- **AND** a new preview under the current trusted policy is required
