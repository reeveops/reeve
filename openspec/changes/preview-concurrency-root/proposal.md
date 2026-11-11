# preview-concurrency-root

## Why

The shared workflow's preview concurrency group keyed on repository and PR
only: two previews for DIFFERENT roots of the same PR (a Pulumi root and a
Terraform/OpenTofu root called separately) landed in one group and cancelled
each other. The docs already admitted the limitation and told users to build
custom composite-action jobs to avoid it.

## What

The preview group includes the root input as its third key. A new preview
for the same root still cancels its predecessor; a new preview for a
different root no longer cancels the still-running one. Command-invocation
runs remain keyed by run id, and drift keeps its root-independent
repository-scoped group.

## Scope

- `.github/workflows/reeve.yml` (group expression + comment).
- `docs/github-actions.md`: the multi-root section documented the cancelled-
  previews limitation as current behavior; it now describes the fix.

## Impact

- No config change.
- Runtime concurrency behavior cannot be unit-tested; a regression guard
  asserts the group expression carries the root key.
