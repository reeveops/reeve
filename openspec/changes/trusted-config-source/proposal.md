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

## Scope

This change defines trusted revision resolution and configuration ownership for PR operations.
Local operations and scheduled drift continue to load the checked-out repository directly.

## Compatibility

An internal PR may still propose control-setting changes, but those changes take effect only after merge.
Workload declarations needed to preview newly added stacks remain head-owned.
