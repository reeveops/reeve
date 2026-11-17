# Trusted configuration source

## Why

Reeve loads all `.reeve` configuration from the checked-out PR HEAD.
A PR can therefore modify the same policy, credentials, executable paths, and outbound sinks intended to constrain that PR.

## What

- Resolve one immutable base commit SHA at run start.
- Load security-control configuration from that trusted revision.
- Load workload declarations from the PR HEAD.
- Merge the two sources through an explicit field ownership table.
- Report PR-side control changes that were ignored for the current run.
- Fail closed when trusted configuration cannot be resolved or validated.
- Resolve the `break_glass:` block from the trusted revision. Add `break_glass.codeowners_from_base` (default `false`) as an independent, opt-in base-SHA lock for the CODEOWNERS content used by the break-glass `codeowners` source. When enabled, PR CODEOWNERS edits are inert until merge; when disabled, existing head-resolved behavior remains unchanged.
- Add a narrowly allowlisted exact-revision read capability for GitHub's recognized CODEOWNERS paths. It fails closed for outside, missing, or unreadable paths and retains all other trusted-file protections.

## Scope

This change defines trusted revision resolution and configuration ownership for PR operations.
Local operations and scheduled drift continue to load the checked-out repository directly.

Ordinary approval CODEOWNERS intentionally remains HEAD-resolved. The optional base-SHA lock is scoped to break-glass emergency authorization; it does not change ordinary review ownership, routing, or approval freshness rules.

## Compatibility

An internal PR may still propose control-setting changes, but those changes take effect only after merge.
Workload declarations needed to preview newly added stacks remain head-owned.

Base-SHA locking for break-glass CODEOWNERS is optional and **off by default**. Existing repositories keep head-resolved CODEOWNERS unless their trusted configuration enables `break_glass.codeowners_from_base`.

## Decision

The `break_glass:` configuration is read from the base revision. The maintainer-approved base-revision direction for its CODEOWNERS source is available through `break_glass.codeowners_from_base: true`, but is not forced on every repository.

With the lock enabled, an actor granted CODEOWNERS ownership only by the PR's HEAD obtains no break-glass authority from that edit until merge. A PR changing both infrastructure and CODEOWNERS still runs Reeve with the base branch as the CODEOWNERS ref. Reviewers and operators authorized on the base can still approve, subject to the ordinary approval rules, and invoke Reeve on that PR.

With the lock disabled, CODEOWNERS remains head-resolved exactly as today. In either mode, `break_glass.reject_self_authorization` retains its existing independent behavior and default `false`: when enabled, it denies break-glass on a PR that touches any authorizing file before source evaluation. The authorizing-file set consistently covers `.reeve/*.yaml`, `.reeve/*.yml`, and all three recognized CODEOWNERS paths.

The `authorizing-config-modified` audit signal remains loud in both modes. Trusted configuration edits are always inert for the current run; CODEOWNERS edits are inert only when the optional base-SHA lock is enabled.
