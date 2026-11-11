# PR flow - deltas

## MODIFIED Requirements

### Requirement: Preview concurrency is scoped per root

The shared workflow's preview concurrency group MUST include the root input,
so previews for different roots of the same PR run concurrently without
cancelling each other. A new preview for the same root cancels its
predecessor.

#### Scenario: Two roots of one PR

- **GIVEN** a repository calling the shared workflow separately for a Pulumi
  root and a Terraform/OpenTofu root
- **WHEN** both roots are previewed in the same push
- **THEN** the two preview runs run concurrently in distinct concurrency
  groups
- **AND** neither cancels the other
