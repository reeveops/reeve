# scaffold-pinned-versions

## Why

`reeve init` wrote `pulumi_version: latest` (or terraform/opentofu) into the
scaffolded workflow caller - and the same `latest` into the manual workflow
printed in next-steps. `latest` installs a moving engine version on the PR
runner: a provider or language behavior change can land unnoticed mid-review,
and the version a PR previewed is not the version the apply runs days later.
The documentation and examples already pin explicit versions in every sample.

## What

The scaffold writes real pinned versions as single-source-of-truth constants
in the scaffold package (used by both the workflow renderer and `init`'s
next-steps text): pulumi `3.262.0`, terraform `1.16.2` (matching the pins the
docs/examples use and the reeve-test harness exercises), opentofu `1.13.9`
(OpenTofu's current stable; the docs do not pin tofu yet - recorded for the
next docs pass). Bumping them is a deliberate change, alongside the docs.

## Scope

- `internal/config/scaffold/scaffold.go` (constants + renderer).
- `cmd/reeve/init.go`: the next-steps manual-workflow text is `reeve init`
  output too; it prints the same pinned constants (single source of truth for
  both surfaces).

## Impact

- New users only: newly scaffolded configs pin real versions instead of
  `latest`.
