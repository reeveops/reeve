# Config - deltas

## ADDED Requirements

### Requirement: Scaffolded callers pin real engine versions

A scaffolded workflow caller MUST name a real engine version
(`pulumi_version`, `terraform_version`, or `opentofu_version`), not a moving
version such as `latest`. The values are repository-maintained constants
shared by the workflow renderer and the `init` next-steps text, and are
bumped deliberately with releases.

#### Scenario: A scaffolded Pulumi caller

- **GIVEN** `reeve init` detects the Pulumi engine
- **WHEN** the workflow caller is rendered
- **THEN** `pulumi_version:` carries the pinned engine version, not
  `latest`
