# Fork preview authorization

## Why

Preview executes PR-controlled IaC and resolves normal preview credentials without considering whether the PR comes from a fork.
An `issue_comment` workflow runs with base-repository privileges, so a trusted commenter can accidentally expose those credentials by asking Reeve to run a fork.

## What

- Deny engine execution and credential acquisition for unapproved forks.
- Add a trusted-comment command that authorizes one exact fork HEAD SHA.
- Add fork-specific preview credential bindings.
- Persist authorization intent before any authorized side effect. Create the deterministic one-shot consumption receipt only immediately before the first authorized side effect, after worker and approved-fork binding prerequisites pass and before credentials or engine execution.
- Keep fork apply and refresh denied without an override path, including for repositories that previously opted in to fork apply.
- Ship the initial release with credentialed fork preview DISABLED outright: every fork preview is validation-only. Later execution requires an authorization for the exact SHA, a capable isolated worker, and a matching approved-fork binding with validated read-only proof. No configuration key, flag, force, or break-glass path enables execution in the RC.

## Scope

This change defines fork classification, authorization, audit, and credential selection.
Trusted configuration loading and OS-level worker isolation remain separate prerequisites for enabling credentialed fork execution.

## Compatibility

Internal PR previews and local previews keep their current behavior. Fork previews are unconditionally validation-only in the RC. Later enablement requires all three prerequisites: exact-SHA authorization, a capable isolated worker, and a matching approved-fork binding with valid read-only proof. The former repository opt-in for fork apply no longer enables fork mutation.

## RC default

**Credentialed fork execution ships disabled: every fork preview is validation-only.** Even a fully authorized fork runs no engine and acquires no credentials in the RC. Authorization intent and the validation-only outcome are still audited, but a run that executes nothing does not create a consumption receipt and does not consume the authorization.

- The authorization command, records, and audit machinery are specified now so enablement is a deliberate later change, not an accident of a missing gate.
- No configuration key, CLI flag, `--force`, or break-glass path enables fork engine execution or fork apply. Enablement requires a spec change naming working isolated-worker and validated approved-fork binding prerequisites, not an operator toggle.
- The action MAY render fork preview results from engine-independent validation only, and states clearly on the PR that engine execution was not run and why.
