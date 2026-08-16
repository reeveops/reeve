# Design

## Command

The command is `/reeve authorize-fork "<justification>" preview` and is accepted only from the configured trusted `author_association` set.
The action passes the actor, comment ID, and exact command text to the binary without evaluating either as shell source.

## Authorization record

The record key is `fork-authorizations/<repo>/<pr>/<head-sha>/<comment-id>.json` in the operator-owned blob store.
It contains the repository, PR number, head SHA, actor, association, justification, run URL, creation time, and consumption state.

Authorization creation uses an absent-object precondition and fails closed when metadata or storage cannot be read or written.
The record is immutable, and consumption creates a separate write-once receipt tied to the preview run ID.

## One-shot semantics

The authorizing command creates the intent record and immediately invokes preview for the same API-resolved HEAD SHA.
Preview atomically creates the consumption receipt before acquiring credentials or starting an engine.

A rerun with the same record cannot acquire credentials because the receipt already exists.
A new PR commit has a different key and requires a new authorization.

## Early trust classification

Preview resolves PR metadata before Pulumi login, enumeration, auth resolution, notification construction, or engine execution.
Metadata failure blocks non-local preview because the run cannot prove whether the PR is a fork.

An unapproved fork may load enough data to render an authorization-required result, but it does not enumerate through an engine binary.
It does not resolve auth bindings, initialize PR-configured network sinks, or persist a successful preview manifest.

## Credential selection

Auth bindings gain `trust: approved_fork` as an additional match dimension.
An approved fork resolves only matching approved-fork bindings and never falls back to ordinary preview or apply bindings.

The configuration validator requires every approved-fork provider to be distinct from providers selected for apply.
Role permissions remain an operator responsibility and documentation labels these credentials as read-only.

## Worker prerequisite

Credentialed fork preview requires a runtime capability flag proving a separate worker identity or equivalent PID and filesystem isolation.
If the capability is absent, authorization is recorded as blocked and no credentials or engine binary are used.

Environment filtering alone does not satisfy this capability.
The first implementation may keep the capability permanently false until the worker boundary ships.

## Apply and refresh

Fork apply and refresh always deny before lock acquisition, auth resolution, policy hooks, and engine execution.
`--force` and break-glass do not alter this rule.

## Audit

Authorization intent and consumption are durable before credential acquisition.
The terminal preview audit links the intent, consumption receipt, actor, SHA, selected trust class, and outcome.
