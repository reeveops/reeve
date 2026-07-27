# Architecture — modularity contract

reeve is a modular core with pluggable providers on every axis: IAC engine,
VCS, auth provider, blob backend, notification channel, and approval source.
This spec is the contract each axis satisfies. It is the review checklist for
any new provider: a provider added "the convenient way" deepens debt that a
later split-builds refactor has to pay off.

## Axes are consumed through interfaces

Every pluggable axis is consumed by core through an interface. Core packages
depend on that interface, never on a concrete provider.

When a core package needs an axis — the drift runner needs an engine, the
run pipeline needs a comment poster — it references the interface type
(`iac.Engine`, `notify.Channel`, `notify.CommentClient`). The concrete
provider is injected by the command wiring in `cmd/reeve`.

Interfaces are consumer-defined and kept narrow. A consumer that needs one
method declares a one-method interface rather than depending on the full
provider surface: `notify.IssueClient` and `notify.CommentClient` are the
reference examples — the `github_issue` and `timeline_github` channels get
GitHub access without importing a VCS SDK.

## Concrete SDKs stay in their provider package

A provider's third-party SDK is imported only within that provider's own
package, never by core. This covers `aws-sdk-go-v2`, `cloud.google.com/go`,
the Azure SDKs, and `google/go-github`.

Grepping a provider SDK import path outside its provider package yields no
matches in core: `go-github` appears only under `internal/vcs/github`, cloud
SDKs only under `internal/auth` and `internal/blob`.

This is enforced mechanically for `internal/core/**` by the `depguard`
`core-purity` rule in `.golangci.yml`, which allows only stdlib and sibling
`internal/core` packages.

## Core does not branch on provider identity

Core does not change behavior based on a provider's name or type string.
`Name()` is display-only.

Differences between providers are expressed through capability flags the
provider advertises — `iac.Capabilities` for engines, optional interface
assertions (`notify.Grouper`) for channels. Behavior that must differ
consults a flag or a type assertion, never `if provider.Name() == "..."`.

## Factories resolve by config; providers self-register

A provider is selected by configuration through a factory that resolves
purely by the config `type:` string. Providers self-register from `init()`
into a registry, so a build can compile in a subset.

A factory does not statically import every concrete provider: that forces
every provider's dependencies into every binary and makes build slicing
impossible. The import manifest lives in a separate `all` package
(`internal/iac/all`, `internal/notify/all`) that commands blank-import; a
build wanting a subset imports the provider packages it needs instead.

A constructor whose optional dependencies are unmet returns `(nil, nil)` and
is skipped, rather than erroring — an unconfigured GitHub token disables the
`github_issue` channel without failing the run. An unregistered `type:` is
always an error naming the registered set.

### Known violation (tracked)

`internal/auth/factory` and `internal/blob/factory` statically import every
concrete provider today, so the AWS, GCP, and Azure SDKs all link into every
binary. The `split-builds` change moves them to self-registration.

## Heavy dependencies are build-tag gated

A provider or feature that pulls in a large dependency sits behind a build
tag, so the default binary does not grow to carry it.

The reference case is the interactive `reeve init` wizard: `charmbracelet/huh`
and its TUI tree are excluded by the `reeve_minimal` build tag, with a stub
that directs the user at `--non-interactive`. A build-tag-gated path is
compiled and tested in CI under its tag — an unbuilt tag rots and only fails
when someone cuts an artifact from it.
