# PR flow - deltas

## ADDED Requirements

### Requirement: Explicit commands are gated on comment events

An explicit `command` action input on `issue_comment` or
`pull_request_review` MUST pass the same bot and `author_association` gates
as a comment command. A refused dispatch MUST exit non-zero and state the
command, the actor, the actor's association, and the configured allowlist.
Other event sources keep dispatching an explicit command unchanged.

#### Scenario: Explicit command from an unauthorized commenter

- **GIVEN** a workflow run on `issue_comment` with `command: drift run`
- **AND** the comment author's `author_association` is `NONE`
- **WHEN** the event is classified
- **THEN** the dispatch refuses with a non-zero exit and nothing downstream
  runs
- **AND** the refusal names the command, the actor, `NONE`, and the
  allowlist that would authorize them

#### Scenario: Explicit command from a workflow-owned event

- **GIVEN** `command: drift run` on `workflow_dispatch` or `schedule`
- **WHEN** the event is classified
- **THEN** the command dispatches unchanged
