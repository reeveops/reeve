# Design

## Why the action and not a reusable workflow

A reusable workflow owns `jobs:`, `runs-on`, and `permissions`, so a caller
cannot interleave a step between checkout and reeve. Real adopters need exactly
that: a private-network join, a credential mint, a repo-specific lint. Every
such need would become another input on a workflow that cannot anticipate them.

Composite-action inputs on the action the caller already invokes add no new
import, keep the caller's `jobs:` block theirs, and leave any step order
possible. Adoption stays one `uses:` line plus inputs.

## Ordering

The new steps run after cloud auth and the Pulumi install, and before reeve
executes: the git rewrite and cache paths must be in `GITHUB_ENV` before the
engine starts, and the prewarm build needs both.

## Where the credential goes

The git rewrite carrying the token is set on the action's own reeve step, not
`GITHUB_ENV`. `GITHUB_ENV` persists for the remainder of the caller's job, so a
token written there is readable by every later step, including steps a consumer
adds after reeve. Scoping it to the one step that starts the engine keeps the
exposure to the process that needs it.

`GOPRIVATE` and the cache paths do go through `GITHUB_ENV`: neither carries a
credential, and a prewarm build in a later step needs them.

reeve's allowlist still governs what the engine inherits, and `env_passthrough`
remains the operator-declared channel. The action stages values for the engine's
environment; it does not widen the allowlist.

## Plugin versions come from go.mod

`prewarm-plugins` takes plugin names, never versions. A version passed in can
drift from the SDK the program compiles against, which is a class of failure the
prewarm exists to prevent. The step reads each version from `go.mod` and fails
loudly when the SDK is absent.

## Reading the Terraform version

The version is one scalar at a known path, and `yq` is not guaranteed on every
runner image, so the step parses it with `awk`. The parser tracks indentation
rather than key order and leaves the `binary:` block when a key at or above its
depth appears, so an unrelated `version:` (the config schema's own top-level
one) is never read. A missing `engine.binary.version` fails the step rather than
installing an unpinned Terraform.

## Both URL forms are rewritten

The git rewrite covers the `https://` and `git@host:` forms. A repository whose
tooling pins the ssh form would otherwise send the engine to a key the runner
does not have, which fails identically to having no credential at all.
