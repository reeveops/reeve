# Design

## Command

The command is `/reeve authorize-fork "<justification>" preview` and is accepted only from the configured trusted `author_association` set.
The action passes the actor, comment ID, and exact command text to the binary without evaluating either as shell source.

## Authorization record

The record key is `fork-authorizations/<repo>/<pr>/<head-sha>/<comment-id>.json` in the operator-owned blob store.
It contains immutable authorization intent: repository, PR number, head SHA, actor, association, justification, run URL, and creation time.

Authorization creation uses an absent-object precondition and fails closed when metadata or storage cannot be read or written.
The record never contains consumption state.

The consumption receipt key is `fork-authorization-consumptions/<repo>/<pr>/<head-sha>/<comment-id>.json`.
Its conditional existence is the sole authoritative consumed or replay state, and its immutable payload records the preview run ID and claim time.

## One-shot semantics

The authorizing command creates the intent record and immediately invokes preview for the same API-resolved HEAD SHA.
Preview creates the deterministic consumption receipt with an absent-object precondition before acquiring credentials or starting an engine.

A concurrent claim or rerun for the same authorization receives a create conflict and cannot acquire credentials or start an engine.
A new PR commit has a different key and requires a new authorization.

## Early trust classification

Preview resolves PR metadata before Pulumi login, enumeration, auth resolution, notification construction, or engine execution.
Metadata failure blocks non-local preview because the run cannot prove whether the PR is a fork.

An unapproved fork may load enough data to render an authorization-required result, but it does not enumerate through an engine binary.
It does not resolve auth bindings, initialize PR-configured network sinks, or persist a successful preview manifest.

## Credential selection

Auth bindings gain `trust: approved_fork` as an additional match dimension.
An approved fork resolves only matching approved-fork bindings and never falls back to ordinary preview or apply bindings.

The approved-fork path requires each selected provider to be distinct from providers selected for apply and to supply provider-specific read-only proof.
Proof MUST bind the exact exchanged identity to a read-only policy through provider API verification or a trusted IAM attestation whose issuer, signature, identity, and policy scope are validated from trusted configuration.

A provider name, role name, configuration boolean, or unauthenticated operator assertion is not proof.
Missing, unsupported, stale, or failed verification rejects the binding before credential acquisition, and the first implementation MAY keep credentialed fork preview disabled until a verifier exists.

## Worker prerequisite

Credentialed fork preview requires a runtime capability flag proving a separate worker identity or equivalent PID and filesystem isolation.
If the capability is absent, authorization is recorded as blocked and no credentials or engine binary are used.

Environment filtering alone does not satisfy this capability.
The first implementation may keep the capability permanently false until the worker boundary ships.

## Pinned worker source

The isolated worker fetches `authorization.head_sha` into its isolated workspace and checks it out in detached mode.
The VCS credential used for the fetch is read-only, is removed before execution, and is never included in the engine environment.

Immediately before approved-fork credential acquisition, the worker verifies that its checked-out commit equals `authorization.head_sha`.
A fetch, checkout, or verification failure records a blocked outcome and prevents credential acquisition and engine execution.

## Apply and refresh

Fork apply and refresh always deny before lock acquisition, auth resolution, policy hooks, and engine execution.
`--force` and break-glass do not alter this rule.

## Audit

Authorization intent and consumption are durable before credential acquisition.
The terminal preview audit links the intent, consumption receipt, actor, SHA, selected trust class, and outcome.
