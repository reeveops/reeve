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
- **THEN** only approved-fork preview bindings are resolved
- **AND** ordinary preview and apply bindings are ignored

#### Scenario: No approved-fork binding

- **WHEN** an authorized fork has no matching approved-fork binding
- **THEN** the engine receives no cloud credentials
