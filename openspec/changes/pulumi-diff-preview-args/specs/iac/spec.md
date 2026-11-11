# IaC Engine Interface - deltas

## ADDED Requirements

### Requirement: Reviewer diff runs with the same plan arguments

A Pulumi adapter that renders the reviewer-facing diff via a second preview
invocation MUST pass the first invocation's refresh flag and caller-supplied
extra arguments to it. The two runs MUST NOT differ in any argument that can
affect the computed change set.

#### Scenario: Refreshed preview with extra args

- **GIVEN** a preview run with `Refresh: true` and extra operator arguments
- **WHEN** the adapter renders the human-readable diff
- **THEN** the second `preview --diff` invocation carries the same
  `--refresh=true` and the same extra arguments
