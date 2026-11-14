# PR flow delta

## MODIFIED Requirements

### Requirement: Fork preview execution is explicitly authorized

An unapproved fork MUST NOT acquire stack credentials or execute an IaC engine.
A credentialed fork preview MUST require a trusted actor, the current
API-resolved HEAD SHA, an unconsumed authorization, and a capable isolated
worker.

#### Scenario: Unapproved fork

- **WHEN** preview resolves a PR as a fork without a valid authorization
- **THEN** no credential provider, policy hook, or IaC engine is invoked
- **AND** the PR receives an authorization-required result

#### Scenario: Current SHA is authorized

- **WHEN** a trusted actor authorizes the current fork HEAD with a justification
- **THEN** authorization intent is durably recorded
- **AND** that exact SHA may run one preview on a capable isolated worker

#### Scenario: Worker executes the authorized source

- **WHEN** an authorized fork preview starts on a capable isolated worker
- **THEN** the worker fetches `authorization.head_sha` and checks it out in detached mode
- **AND** the source-fetch credential is removed before engine execution
- **AND** the worker verifies the checked-out commit equals `authorization.head_sha` immediately before credential acquisition
- **AND** the engine runs only from that verified working tree

#### Scenario: Worker source does not match the authorization

- **WHEN** the authorized SHA cannot be fetched or the checked-out commit differs from `authorization.head_sha`
- **THEN** preview records a blocked outcome before credential acquisition or engine execution

#### Scenario: Fork HEAD changes

- **WHEN** the fork HEAD differs from the SHA in the authorization
- **THEN** preview is denied before credential acquisition

#### Scenario: Authorization is replayed

- **WHEN** the deterministic consumption receipt already exists or a concurrent create wins for the authorization
- **THEN** preview is denied before credential acquisition

#### Scenario: PR metadata cannot be resolved

- **WHEN** the VCS cannot establish fork status and current HEAD SHA
- **THEN** non-local preview fails before credential acquisition or engine execution

#### Scenario: Intent or consumption audit fails

- **WHEN** authorization intent or consumption cannot be written durably
- **THEN** preview fails before credential acquisition or engine execution

#### Scenario: Worker isolation is unavailable

- **WHEN** a fork authorization is valid but the runtime cannot isolate the worker process and files
- **THEN** preview records a blocked outcome without credentials or engine execution

### Requirement: Fork mutation is prohibited

Fork PR apply and refresh MUST be denied before credentials, locks, policy hooks, or engine execution.
No force or break-glass option may bypass this prohibition.

#### Scenario: Fork apply uses force

- **WHEN** any fork PR invokes apply with `--force`
- **THEN** the operation is denied before credentials or engine execution

#### Scenario: Fork apply uses break-glass

- **WHEN** any fork PR invokes break-glass apply
- **THEN** the operation is denied before authorization policy hooks or credentials

## ADDED Requirements

### Requirement: Credentialed fork execution ships disabled

The initial release MUST keep fork engine execution disabled outright: every
fork preview runs validation-only. No configuration key, command-line flag,
`--force`, or break-glass path enables fork engine execution; enabling it is
a spec change that names both prerequisites - a working isolated worker and
validated approved-fork read-only bindings.

#### Scenario: Authorized fork under the RC default

- **GIVEN** a valid one-shot authorization for the fork's current HEAD
- **WHEN** the fork preview starts under the RC default (no isolated-worker
  prerequisite exists yet)
- **THEN** no credential provider is resolved and no IaC engine is invoked
- **AND** the PR result states that engine execution did not run and why
- **AND** the authorization is NOT consumed: the run executed nothing under
  it, so the same authorization can still be spent by a later run that can
  actually execute (the consumption is recorded when a run uses the
  authorization to acquire credentials or start an engine)

#### Scenario: No overriding knob exists

- **GIVEN** any configuration, flag, force, or break-glass setting
- **WHEN** a fork preview runs
- **THEN** the engine is not invoked
- **AND** the validation-only outcome and its reason are stated on the PR
