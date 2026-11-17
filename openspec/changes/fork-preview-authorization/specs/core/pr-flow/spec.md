# PR flow delta

## ADDED Requirements

### Requirement: Fork preview execution is explicitly authorized

An unapproved fork MUST NOT acquire stack credentials or execute an IaC engine.
A credentialed fork preview MUST require a trusted actor, the current
API-resolved HEAD SHA, an unconsumed authorization, a capable isolated worker,
and a matching approved-fork binding with valid read-only proof. The
deterministic consumption receipt MUST be created only immediately before the
first authorized side effect, after worker and binding prerequisites pass and
before credential acquisition or engine execution. Validation-only and earlier
blocked runs MUST NOT create a receipt or consume the authorization. Terminal
audit MUST record the authorization intent and outcome even when no receipt
exists.

#### Scenario: Unapproved fork

- **WHEN** preview resolves a PR as a fork without a valid authorization
- **THEN** no credential provider, policy hook, or IaC engine is invoked
- **AND** the PR receives an authorization-required result

#### Scenario: Current SHA is authorized

- **WHEN** a trusted actor authorizes the current fork HEAD with a justification
- **THEN** authorization intent is durably recorded
- **AND** that exact SHA may run one preview on a capable isolated worker

#### Scenario: Consumption occurs immediately before the first authorized side effect

- **GIVEN** exact-SHA authorization, worker isolation, and approved-fork binding read-only proof have passed
- **WHEN** preview is about to acquire the read-only source-fetch credential
- **THEN** it creates the deterministic consumption receipt with an absent-object precondition immediately beforehand
- **AND** no receipt exists before the worker and binding prerequisites pass
- **AND** all credential acquisition and engine execution occur after the receipt is durable

#### Scenario: A prerequisite failure leaves authorization unconsumed

- **WHEN** worker isolation, approved-fork binding selection, or read-only proof fails before the first authorized side effect
- **THEN** no consumption receipt is created and the authorization remains unconsumed
- **AND** the terminal audit records the authorization intent and blocked outcome without a receipt

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

- **WHEN** authorization intent cannot be written durably, or the consumption receipt cannot be created at the first-side-effect boundary
- **THEN** preview fails before credential acquisition, source fetch, or engine execution
- **AND** a validation-only run that needs no receipt still records its terminal authorization intent and outcome

#### Scenario: Worker isolation is unavailable

- **WHEN** a fork authorization is valid but the runtime cannot isolate the worker process and files
- **THEN** preview records a blocked outcome without credentials or engine execution
- **AND** no consumption receipt is created
- **AND** the terminal audit records the authorization intent and blocked outcome without a receipt

### Requirement: Fork mutation is prohibited

Fork PR apply and refresh MUST be denied before credentials, locks, policy hooks, or engine execution.
No force or break-glass option may bypass this prohibition.

#### Scenario: Fork apply uses force

- **WHEN** any fork PR invokes apply with `--force`
- **THEN** the operation is denied before credentials or engine execution

#### Scenario: Fork apply uses break-glass

- **WHEN** any fork PR invokes break-glass apply
- **THEN** the operation is denied before authorization policy hooks or credentials

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
- **AND** no consumption receipt is created and the authorization is NOT consumed
- **AND** the terminal audit records the authorization intent and validation-only outcome without a receipt
- **AND** the same authorization can still be spent by a later run whose worker and binding prerequisites pass and which reaches the first authorized side effect

#### Scenario: No overriding knob exists

- **GIVEN** any configuration, flag, force, or break-glass setting
- **WHEN** a fork preview runs
- **THEN** the engine is not invoked
- **AND** the validation-only outcome and its reason are stated on the PR
