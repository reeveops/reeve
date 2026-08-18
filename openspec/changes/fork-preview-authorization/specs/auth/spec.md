# Auth delta

## MODIFIED Requirements

### Requirement: Fork credentials use an isolated binding class

Fork PRs MUST receive no credentials by default.
An authorized fork preview MUST resolve only bindings explicitly matched to `trust: approved_fork`.

#### Scenario: Internal preview

- **WHEN** an internal PR runs preview
- **THEN** ordinary preview bindings are resolved
- **AND** approved-fork bindings are ignored

#### Scenario: Approved fork preview

- **WHEN** a valid one-shot authorization is consumed for the current fork HEAD
- **AND** every selected approved-fork provider presents valid read-only proof for its exact exchanged identity
- **THEN** only approved-fork preview bindings are resolved
- **AND** ordinary preview and apply bindings are ignored

#### Scenario: Approved-fork permission proof is unavailable

- **WHEN** an approved-fork binding lacks supported and valid read-only proof
- **THEN** configuration validation rejects the binding before credential acquisition
- **AND** a provider label, role name, or self-declared boolean does not satisfy the requirement

#### Scenario: No approved-fork binding

- **WHEN** an authorized fork has no matching approved-fork binding
- **THEN** no credential provider is resolved or invoked
- **AND** no IaC engine is invoked
- **AND** validation-only processing receives an allowlisted environment with no secret-bearing values
