# Approvals (break-glass) - deltas

## REMOVED Requirements

### Requirement: Break-glass authorization is head-resolved and self-add is flagged, not forbidden

The head-resolved model is retired: resolving the authorization surface from
the PR HEAD let a PR grant itself emergency override authority - the
`reject_self_authorization` hole this proposal closes.

## ADDED Requirements

### Requirement: Break-glass authorization resolves from the trusted revision

Break-glass authorization MUST resolve its entire surface from the trusted
configuration revision - the `break_glass:` block in shared.yaml AND the
CODEOWNERS content the `codeowners` source consults - and never from PR HEAD.
A PR that adds an actor to `break_glass.authorized` or grants itself
CODEOWNERS ownership of a changed path MUST NOT authorize its own emergency
override: the addition takes effect only after merge. No new configuration
keys are introduced by this change.

#### Scenario: A PR grants itself break-glass authority

- **GIVEN** a PR whose HEAD adds the author to `break_glass.authorized`
  under `internal_list`
- **WHEN** the PR runs `/reeve breakglass`
- **THEN** authorization resolves against the trusted revision, where the
  author is not listed
- **AND** the apply is denied with a trace naming every source consulted
- **AND** the audit record flags that the PR modified its own authorizing
  config

#### Scenario: A PR grants itself a CODEOWNERS path

- **GIVEN** a PR whose HEAD adds a CODEOWNERS entry making the author an
  owner of a changed path
- **WHEN** the PR runs `/reeve breakglass` relying on the `codeowners`
  source
- **THEN** CODEOWNERS is read from the trusted revision, where the entry is
  absent
- **AND** the `codeowners` source does not grant, and the apply is denied
  unless another trusted source grants

#### Scenario: reject_self_authorization distrusts the toucher

- **GIVEN** `break_glass.reject_self_authorization: true`
- **AND** an actor listed in the trusted revision's
  `break_glass.authorized`
- **WHEN** that actor runs `/reeve breakglass` from a PR that modified a
  `.reeve` config or CODEOWNERS file
- **THEN** the apply is denied before any source is evaluated
- **AND** the trace names the touched paths

#### Scenario: The modification flag still records the attempt

- **GIVEN** a PR that modifies a `.reeve/*.yaml` config or any CODEOWNERS
  file
- **WHEN** a break-glass apply runs on that PR
- **THEN** the audit record flags `authorizing-config-modified` with the
  touched paths
- **AND** the modification is inert for the current run

The `authorizing-config-modified` audit signal stays: the modification is
inert for the current run, and the audit record still names every touched
path. `break_glass.reject_self_authorization` stays accepted and keeps
enforceable meaning: when set, a PR that modifies any authorizing file is
denied break-glass outright, before any source evaluation, with the touched
paths in the trace. Source-union matching, narrowest-first recording, and
denial traces are unchanged.
