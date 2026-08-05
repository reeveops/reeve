# Architecture — modularity contract delta

## ADDED Requirements

### Requirement: Provider axes are consumed through interfaces

Every pluggable axis SHALL be consumed by core through an interface, and core
packages SHALL depend on that interface rather than any concrete provider
implementation. The axes are: IAC engine, VCS, auth provider, blob backend,
notification channel, and approval source.

#### Scenario: Core depends on the interface

- **WHEN** a core package needs an axis (e.g. the drift runner needs an engine)
- **THEN** it references the interface type (e.g. `iac.Engine`), and the
  concrete provider is injected by the command wiring, not imported by core

### Requirement: Concrete SDKs stay in their provider package

A provider's third-party SDK SHALL be imported only within that provider's own
package and SHALL NOT be imported by core packages. This covers, for example,
`aws-sdk-go-v2`, `cloud.google.com/go`, and `google/go-github`.

#### Scenario: SDK import confined

- **WHEN** grepping for a provider SDK import path outside its provider package
- **THEN** there are no matches in core (e.g. `go-github` appears only under
  `internal/vcs/github`, cloud SDKs only under `internal/auth` and
  `internal/blob`)

### Requirement: Core does not branch on provider identity

Core SHALL NOT change behavior based on a provider's name or type string
(`Name()` is display-only). Differences between providers SHALL be expressed
through capability flags the provider advertises.

#### Scenario: Capability flag, not a name check

- **WHEN** behavior must differ for a provider that supports a feature (e.g.
  comment edit-in-place, stale-review dismissal)
- **THEN** core consults a capability flag/interface, not `if provider.Name() == "..."`

### Requirement: Factories resolve by config; providers self-register

A provider is selected by configuration through a factory. Providers SHALL
self-register (e.g. via `init()` or an explicit registry) so a build can
include a subset of providers. A factory SHALL NOT statically import every
concrete provider, because that forces every provider's dependencies into
every binary and makes build slicing impossible.

#### Scenario: A build can exclude a provider

- **WHEN** a build is configured (e.g. via build tags) to include only the AWS
  provider
- **THEN** it compiles without linking the GCP or Azure SDKs, and the factory
  resolves only the registered providers

> Known current violation (tracked, not resolved by this spec):
> `internal/auth/factory` and `internal/blob/factory` statically import all
> providers today. The `split-builds` change moves them to self-registration.

### Requirement: Heavy new dependencies are build-tag gated

A new provider that pulls in a large dependency SHALL be placed behind a build
tag or in a separate artifact, so the default binary does not grow to carry
it. An identity-directory SDK for external group lookups is one such case.

#### Scenario: Heavy provider does not bloat the default build

- **WHEN** a provider requiring a heavy SDK is added
- **THEN** it is gated behind a build tag, and the default `reeve` binary is
  built without it unless explicitly requested
