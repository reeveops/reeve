# PR flow delta

## ADDED Requirements

### Requirement: Preview reports whether its plan was explicitly requested

Preview MUST carry, in the PR-flow events it publishes, whether the plan was
explicitly requested rather than triggered by a commit update.

This signal MUST be an explicit input to preview. Preview MUST NOT derive it by
inspecting VCS provider event names.

#### Scenario: Operator comments a plan command

- **WHEN** the workflow invokes preview for an explicit plan request
- **THEN** the published plan event marks the plan as explicitly requested

#### Scenario: Commit pushed

- **WHEN** preview runs because the PR head changed
- **THEN** the published plan event does not mark an explicit request
