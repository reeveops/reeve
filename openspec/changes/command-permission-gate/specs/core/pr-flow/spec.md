# PR flow - deltas

## ADDED Requirements

### Requirement: The level is read from the response's permission flags

Every `/reeve` command dispatch and every counted `pr_comment` approval MUST
require the comment actor to hold at least the repository permission named by
`required-permission`. The actor's level MUST be read from the
`user.permissions` object of the collaborator-permission response
(`GET /repos/{owner}/{repo}/collaborators/{username}/permission`), which
carries the boolean flags `admin`, `maintain`, `push`, `triage`, and `pull`.
GitHub documents that this hash contains the BASE role permissions of the
collaborator, so it is correct for a holder of a CUSTOM repository role too
(a custom role inherits a predefined base role and the hash reflects that
base). The level maps one-to-one: `admin` flag → `admin`; `maintain` flag
(with `admin` false) → `maintain`; `push` flag → `write`.
`triage`/`pull`-only or none of the flags → below the bar, which any accepted
`required-permission` value denies. The response's top-level `permission`
string MUST NOT be used for the level (it is the legacy field that folds
`maintain` into `write`), and `role_name` MUST be recorded in traces and
audit records as the assigned role - it is no longer the level source. The
permission MUST be resolved through the source repository's
collaborator-permission API and cached per invocation. A denial MUST name the
actor, the association that passed, the observed level, and the required
level.

#### Scenario: The level comes from the permission flags

- **GIVEN** the collaborator-permission response carries `maintain: true`
  with `admin: false` for the actor
- **WHEN** an actor whose required level is `write` dispatches a command
- **THEN** the level maps to `maintain` and the command runs
- **AND** the trace names the observed level and the assigned `role_name`

#### Scenario: A custom repository role

- **GIVEN** the collaborator holds a custom repository role whose base role
  is `write`, so the flags carry `push: true` and `maintain: false` while
  `role_name` names the custom role
- **WHEN** an actor whose required level is `write` dispatches a command
- **THEN** the level maps to `write` from the base-role flags and the
  command runs
- **AND** the trace and audit record name the custom `role_name` verbatim -
  a custom role is NEVER refused merely for having a name the mapping does
  not recognize

#### Scenario: A response that cannot be interpreted

- **GIVEN** the collaborator-permission response is missing the
  `user.permissions` object, the `user` is `null`, or the object cannot be
  parsed
- **WHEN** any command is dispatched for that actor
- **THEN** the dispatch refuses, naming exactly what the response carried
  (for example: no permissions object, or a null collaborator) and the
  required level
- **AND** nothing assumes a level from an uninterpretable response

#### Scenario: Org member without repo access

- **GIVEN** the actor's `author_association` is `MEMBER`
- **AND** their mapped level is below `required-permission`
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

### Requirement: The permission bar resolves from trusted configuration, with the CLI flag above it

The bar MUST have three sources with a fixed precedence: the
`--required-permission` CLI flag (highest), the
`approvals.required_permission` config key read from TRUSTED configuration,
and the `required-permission` action input (lowest). The trusted config key
SHALL override the action input whether it is explicitly set or left at its
default, because the action input rides the caller's workflow file while the
config key is the operator's reviewed repository authority. When a config
key and an explicitly-set action input disagree, the config key MUST WIN and
the run's log MUST state which source supplied the effective value. An
unresolvable value at ANY layer (the flag, the config key, or the action
input) MUST fail closed at load, naming the layer and the value.

#### Scenario: The flag overrides both

- **GIVEN** the config key and the action input are both set (the config key
  to `maintain`, the action input to `write`)
- **WHEN** a run starts with `--required-permission=admin`
- **THEN** the effective bar is `admin`, and the log states the flag
  supplied it

#### Scenario: The config key overrides an explicitly-set action input

- **GIVEN** the config key is set to `maintain` and the action input is
  explicitly set to `write` on the workflow
- **WHEN** a run starts without the flag
- **THEN** the effective bar is `maintain`, read from TRUSTED configuration,
  and the log states the config key supplied it and that the action input's
  value was overridden

#### Scenario: An invalid value at each layer

- **GIVEN** a value outside the accepted set (`write`, `maintain`, `admin`)
  - on the CLI flag, in the config key, or on the action input
- **WHEN** the run loads the value
- **THEN** the load fails closed, naming the layer the value came from and
  the value itself
- **AND** nothing dispatches or approves with an unknown bar
