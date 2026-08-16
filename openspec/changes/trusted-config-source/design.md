# Design

## Immutable trusted revision

The VCS PR shape gains the base commit SHA returned by the same metadata request that returns fork status and head SHA.
Every PR operation snapshots that SHA once and never resolves a moving branch name again during the run.

The VCS adapter reads `.reeve` files at the exact base SHA through the repository contents API.
The adapter rejects symlinks, submodules, oversized files, duplicate config types, and paths outside `.reeve`.

## Two parsed configurations

The existing strict decoder parses trusted bytes and checked-out HEAD bytes independently before any merge.
Unknown keys, invalid versions, missing required trusted files, or API failures stop the operation before credentials or configurable subprocesses.

Environment references in trusted configuration expand from the controller environment only after source ownership is established.
Environment references in head-owned workload fields remain prohibited unless a specific field is added to the ownership table.

## Field ownership

Trusted fields come only from the immutable base SHA:

- Bucket, lock, retention, and audit-store settings.
- Approval, precondition, freeze, apply-trigger, fork, and break-glass policy.
- Auth providers, bindings, state-auth providers, and credential durations.
- Notification channels, webhook destinations, OTEL exporters, and secret references.
- Engine binary overrides, policy-hook commands, and plugin or registry configuration.
- Redaction configuration and any future execution-policy settings.

Head-owned fields describe the workload being evaluated:

- Engine type when it matches the trusted engine type.
- Stack declarations, stack paths, environments, and project names.
- Change-mapping include, exclude, and dependency declarations.
- Engine project metadata that cannot select an executable or credential.

Fields not listed are trusted by default.
Adding a head-owned field requires a spec delta and a security test.

## Merge behavior

The merge produces a new effective configuration and never mutates either parsed input.
It records source metadata for each config type and the trusted base SHA in the run manifest and audit entry.

A missing trusted config type does not fall back to the PR version.
Repository bootstrap requires the configuration to land on the base branch before PR automation can use it.

## Visibility

Changed-file analysis identifies PR edits to trusted fields and config files.
The preview comment lists those edits as proposed changes that are ignored for the current run and take effect after merge.

Logs and comments do not render secret values or expanded environment references.
All messages pass through the existing redactor.

## Apply consistency

Preview manifests record both head SHA and trusted base SHA.
Apply requires the current trusted policy SHA to match the preview manifest or requires a new preview.

This prevents an approval under one base policy from being applied after that policy changes.
The normal up-to-date gate remains independent.

## Local and drift behavior

Local commands have no PR trust boundary and load the working tree as before.
Drift runs against the repository revision checked out by the trusted scheduled workflow and record that commit SHA.
