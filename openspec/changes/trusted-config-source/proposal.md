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
- Resolve the entire break-glass authorization surface - `break_glass.*` and the CODEOWNERS its `codeowners` source consults - from the trusted revision. A PR can no longer authorize its own emergency override: the modification takes effect only after merge. `break_glass.reject_self_authorization` stays accepted and gains a strictly enforceable meaning (deny break-glass in a PR that touches its own authorizing files).

## Scope

This change defines trusted revision resolution and configuration ownership for PR operations.
Local operations and scheduled drift continue to load the checked-out repository directly.

## Compatibility

An internal PR may still propose control-setting changes, but those changes take effect only after merge.
Workload declarations needed to preview newly added stacks remain head-owned.
The documented head-resolved break-glass self-authorization ("self-add flagged, not forbidden") is intentionally retired by this change: it is the same `reject_self_authorization` hole this proposal closes, and no new keys are introduced - `reject_self_authorization` keeps working with a strictly enforceable meaning.
