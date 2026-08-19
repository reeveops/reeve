# Action engine preparation

## Why

`child-env-isolation` builds every engine subprocess environment from a small
allowlist plus stack-bound credentials. Correct, and it has two consequences
every private-module repository hits:

- The engine cannot read the runner's `~/.gitconfig`, so a `git config --global`
  URL rewrite is invisible to it and a private module fetch fails with
  `could not read Username for 'https://github.com'`.
- `GOMODCACHE` and `GOCACHE` resolve inside the engine's isolated `HOME`, so a
  cache the workflow warmed is invisible and every module re-downloads inside
  the run, against the engine's own preview timeout.

Adopters currently rediscover both, then hand-write the same git-config and
cache-export shell in every workflow. Compiling a large program inside the
preview timeout and pinning a Terraform version by hand are the same shape of
undifferentiated wiring.

## What

Add opt-in inputs to the shipped action so one `uses:` line covers the whole
setup:

- `private-modules-token`, `private-modules-host`, `go-private` - configure the
  git URL rewrite and `GOPRIVATE`, exported so the engine subprocess sees them.
- `go-cache` - export the runner's resolved `GOMODCACHE` and `GOCACHE`.
- `prewarm-dir`, `prewarm-plugins` - build the program and install Pulumi
  plugins before reeve runs, outside the engine's preview timeout. Plugin
  versions are read from `go.mod`.
- `terraform-version-from-config`, `terraform-config-path` - install Terraform
  pinned to `engine.binary.version` so runtime cannot drift from config.

Every input is off by default. With none set, behavior is unchanged.

## Scope

In: action inputs and the composite steps behind them, plus the docs and
example that explain them.

Out: any change to how reeve itself builds the child environment. The allowlist,
the isolated `HOME`, and `env_passthrough` are untouched.

## Security boundary

`private-modules-token` widens what the engine can reach. `child-env-isolation`
states that environment filtering "does not isolate hostile code from the
runner's process table or filesystem", so an IaC program on the PR branch can
read a credential placed in its environment.

That makes this input a deliberate, operator-chosen widening, not a safe
default. It is off unless set, it warns on every run that uses it, and its
documentation requires scoping the token to the module repositories with
read-only access. The existing alternative stands: keep the credential in the
caller's workflow and forward it with `env_passthrough`.

This input does not make fork execution safe. Fork denial and authorization
remain as `child-env-isolation` and `fork-preview-authorization` define them.
