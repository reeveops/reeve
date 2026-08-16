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

## Scope

This change defines fork classification, authorization, audit, and credential selection.
Trusted configuration loading and OS-level worker isolation remain separate prerequisites for enabling credentialed fork execution.

## Compatibility

Internal PR previews and local previews keep their current behavior.
Fork previews become validation-only until an authorization is present and the worker boundary reports that it can isolate hostile code.
