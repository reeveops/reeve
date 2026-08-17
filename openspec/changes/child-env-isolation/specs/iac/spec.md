# IaC Engine Interface - deltas

## ADDED Requirements

### Requirement: Subprocess environment

Every engine subprocess MUST receive a constructed environment containing only
approved operational variables and values explicitly supplied in operation
options. It MUST NOT inherit reeve's full process environment.

#### Scenario: Ambient controller credential

- **GIVEN** reeve's process environment contains a controller credential
- **AND** the credential was not explicitly supplied in the engine options
- **WHEN** any engine operation starts a subprocess
- **THEN** the subprocess environment does not contain that credential

#### Scenario: Bound stack credential

- **GIVEN** auth resolution supplies a credential in the engine options
- **WHEN** the engine starts a subprocess
- **THEN** the subprocess receives that credential
- **AND** the explicit value overrides an allowlisted ambient value with the
  same key

#### Scenario: CI home discovery

- **GIVEN** reeve is running in CI
- **WHEN** an engine subprocess starts
- **THEN** its home and XDG paths point to a private run-scoped directory
- **AND** reeve removes that directory after the engine run exits

### Requirement: State backend authentication

When `engine.state.auth_provider` is set, reeve MUST acquire that declared
provider before backend login and MUST include its environment in engine calls.

#### Scenario: Pulumi Cloud login

- **GIVEN** a state auth provider supplies `PULUMI_ACCESS_TOKEN`
- **WHEN** reeve runs `pulumi login`
- **THEN** the login process receives that explicit token
- **AND** it does not receive an ambient token from reeve's environment
