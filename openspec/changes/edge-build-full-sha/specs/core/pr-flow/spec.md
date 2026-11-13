# PR flow - deltas

## ADDED Requirements

### Requirement: Edge artifacts are immutable per commit

Per-push (edge) binary artifacts MUST carry the full commit SHA in their
names and tags, and a published edge release MUST NOT be overwritten:
builds embed the build date, so re-uploading replaces signed artifacts with
differently-signed bytes. The binary resolver MUST resolve the full-SHA asset
names for both edge and commit callers.

#### Scenario: A rerun of an already-published commit

- **GIVEN** an edge release whose assets are already published
- **WHEN** the edge-build workflow runs again for the same commit
- **THEN** no asset is overwritten
- **AND** the run stops with a visible note naming the release

#### Scenario: The action resolves an edge binary

- **GIVEN** the action pinned to a branch or full commit SHA
- **WHEN** the binary resolver selects the source-matched edge release
- **THEN** it downloads the asset named with the FULL commit SHA
