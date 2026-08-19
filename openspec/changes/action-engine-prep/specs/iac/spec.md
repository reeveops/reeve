# IaC delta

## ADDED Requirements

### Requirement: Engine preparation is opt-in and defaults to unchanged behavior

Runner preparation MUST be off unless the operator sets its input, for every
preparation the shipped action offers: private module access, a warmed build
cache, a pre-compiled program, a pinned engine binary. With none set, the
action's behavior MUST be unchanged.

A preparation step that cannot do its job MUST fail with a message naming what
is missing, rather than continuing and letting the engine fail later.

#### Scenario: No preparation inputs set

- **WHEN** the action runs with none of the preparation inputs set
- **THEN** no git configuration, cache path, or prewarm step runs

#### Scenario: Cache export without a Go toolchain

- **WHEN** cache export is requested and Go is not on PATH
- **THEN** the step fails naming the missing toolchain

#### Scenario: Pinned engine version absent from config

- **WHEN** the engine version is to be read from config and the field is unset
- **THEN** the step fails rather than installing an unpinned engine binary

### Requirement: Credential preparation is announced and operator-chosen

A credential preparation input MUST be off by default and MUST emit a warning on
every run that uses it, because it places that credential where the engine
subprocess can read it.

Its documentation MUST state that an IaC program from the pull request branch
can read that credential, and MUST direct the operator to scope it to the
narrowest read-only access that satisfies the fetch.

The action MUST NOT bypass the child environment allowlist to deliver such a
credential: it MUST stage the value where the operator's declared configuration
decides whether the engine receives it.

#### Scenario: Private module credential in use

- **WHEN** a run uses the private-module credential input
- **THEN** the run warns that the credential reaches the engine subprocess

#### Scenario: Credential set without a module scope

- **WHEN** the credential is set without the corresponding module path setting
- **THEN** the run warns that fetches still route through the public proxy

### Requirement: Pinned tool versions derive from the repository

A prepared plugin or engine version MUST be read from the repository's own
manifest or engine config, never accepted as a version input.

#### Scenario: Plugin prewarm

- **WHEN** plugins are pre-installed by name
- **THEN** each version is read from the repository's module manifest
- **AND** a name whose dependency is absent fails the step
