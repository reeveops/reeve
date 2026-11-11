# PR flow - deltas

## ADDED Requirements

### Requirement: Commands require real repository permission

Every `/reeve` command dispatch and every counted `pr_comment` approval MUST
require the comment actor to hold at least the repository permission named by
the `required-permission` action input (default `write`). The permission MUST
be resolved through the source repository's collaborator-permission API and
cached per invocation. A denial MUST name the actor, the association that
passed, the observed level, and the required level.

#### Scenario: Org member without repo access

- **GIVEN** the actor's `author_association` is `MEMBER`
- **AND** their effective permission is below `required-permission`
- **WHEN** the actor dispatches `/reeve apply`
- **THEN** the command is refused, naming the actor, the association, the
  observed level, and the required level
- **AND** nothing runs

#### Scenario: The permission lookup fails

- **GIVEN** the collaborator-permission API errors for the actor
- **WHEN** the actor dispatches any `reeve` command
- **THEN** the dispatch refuses as if the permission were below the level
- **AND** the refusal states that the permission could not be evaluated

#### Scenario: An unknown actor

- **GIVEN** the collaborator-permission API reports no relation for the
  actor
- **WHEN** the actor dispatches any `reeve` command
- **THEN** the permission is treated as `none` and the command is refused

#### Scenario: Workflow-owned events are unchanged

- **GIVEN** `command:` input supplied by `workflow_dispatch`, `schedule`, or
  `push`
- **WHEN** the event is classified
- **THEN** dispatch proceeds without the comment-actor permission check

### Requirement: allowed-associations remains the prefilter

`allowed-associations` MUST keep its semantics: it filters first, with
unchanged defaults and denial behavior, and a failed association denies
before any permission lookup. A permission failure after a passing
association MUST state that the association passed but the permission did
not.

#### Scenario: Association already denies

- **GIVEN** the actor's association is `NONE` in the default allowlist
- **WHEN** the actor dispatches any `reeve` command
- **THEN** the refusal is the existing association denial, unchanged
- **AND** no permission lookup is made
