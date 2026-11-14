# Fork preview authorization

## Why

Preview executes PR-controlled IaC and resolves normal preview credentials without considering whether the PR comes from a fork.
An `issue_comment` workflow runs with base-repository privileges, so a trusted commenter can accidentally expose those credentials by asking Reeve to run a fork.

## What

- Deny engine execution and credential acquisition for unapproved forks.
- Add a trusted-comment command that authorizes one exact fork HEAD SHA.
- Add fork-specific preview credential bindings.
- Persist authorization intent before engine execution and consume it once.
- Keep fork apply and refresh denied without an override path.
- Ship the initial release with credentialed fork preview DISABLED outright: an authorization authorizes one exact SHA, but engine execution for a fork additionally requires the worker-isolation capability and validated approved-fork read-only bindings, neither of which has an implementation. Until both exist, every fork preview is validation-only - even an authorized one - and no configuration key, flag, force, or break-glass path enables execution.

## Scope

This change defines fork classification, authorization, audit, and credential selection.
Trusted configuration loading and OS-level worker isolation remain separate prerequisites for enabling credentialed fork execution.

## Compatibility

Internal PR previews and local previews keep their current behavior.
Fork previews become validation-only until an authorization is present and the worker boundary reports that it can isolate hostile code.

## RC default (decision)

The review of 2026-11-10 makes the initial release candidate decision explicit: **credentialed fork execution ships disabled - every fork preview is validation-only.** Fork previews do not run the engine until **both** a trusted commenter authorises the exact SHA **and** a capable isolated worker (whose checks the authorization consumes) is in place; and for the RC, even a fully authorized fork does not run the engine because neither prerequisite has an implementation - so the authorization alone executes nothing, and the PR result says so.

- The authorization command, records, and audit machinery above are specified now so enablement is a deliberate later change, not an accident of a missing gate.
- No configuration key, CLI flag, `--force`, or break-glass path enables fork engine execution. Enablement requires a spec change that names a working isolated-worker prerequisite, not an operator toggle.
- The action MAY render fork preview results from engine-independent validation only, and states clearly on the PR that engine execution was not run and why.
