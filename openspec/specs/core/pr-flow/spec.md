# PR Flow

## Overview

On PR open or update, reeve runs **preview** for every stack touched. A single
PR comment is posted and edited in place on subsequent runs. On `/reeve apply`
comment (or merge, depending on config), reeve acquires locks and runs **apply**.

## Pipeline

1. PR opened / updated → `reeve run preview` for touched stacks.
2. Single PR comment posted, identified by hidden HTML marker, edited in place
   on subsequent runs. Help comment upserted separately.
3. Slack message posted/updated (if configured).
4. A subscribed `ready_for_review` event invokes ready when a successful plan exists.
   `/reeve ready` is also available explicitly; `auto_ready` does not implement a dispatch gate.
5. Reviewers approve per configured rules.
6. Apply is initiated per `apply.trigger` (see Apply trigger modes below):
   `comment` → on `/reeve apply`; `merge` → on PR merge. Either way reeve
   acquires locks, evaluates preconditions, and runs apply.
6a. Apply posts a per-run timeline comment, starting with `🚀 apply starting` and appending each event (see rendering spec).
7. Results update PR comment and Slack message.
8. Audit log entry written to bucket.
9. Locks released, queue advanced.

## Requirements

- Preview runs independent project directories concurrently up to
  `engine.execution.max_parallel_stacks`; zero or omission defaults to one.
- A positive `--max-parallel-stacks` value overrides the config for one run.
- Stacks sharing one project directory run serially because their engine
  working data and workspace selection share that directory.
- Preview results, failure lists, and manifests retain discovery order.
- Preview artifacts persist under `runs/pr-{n}/{run-id}/` until configured retention or explicit removal.
- A CI run ID includes the provider's run attempt when one is available. A
  rerun of the same run number MUST write a distinct manifest, saved-plan
  prefix, and audit record.
- New run IDs MUST include the full commit SHA. Preview selection MUST accept
  legacy short-SHA IDs and skip them when the manifest names a different full
  commit SHA.
- Apply and refresh lock-holder identity MUST include the provider attempt.
  A retry MUST NOT adopt an unexpired lease from an earlier attempt.
- A supplied run attempt MUST be a positive integer. Invalid flag or
  environment values MUST fail before artifact, lock, or audit identity is
  generated.
- Apply MUST reject unreadable or malformed identity-bound preview history for
  the selected commit and MUST NOT fall back to an older valid candidate.
- Readiness MUST skip its notification when preview history is unavailable.
  Explain MUST render a fail-closed diagnostic report with the storage error.
- Apply reuses the preview's saved engine plan by default when available. Missing or unreadable plan artifacts fall back to a fresh plan with an explicit timeline warning; `--refresh` and `engine.plan_locking: false` disable saved-plan reuse.
- Preview-manifest integrity is separate: malformed or unreadable selected history fails closed except for the explicitly authorized recovery path below.
- Apply on fork PRs is denied by default. The gate does not reduce preview credentials; previews use configured preview bindings subject to workflow credential availability.
- Start and completion notifications follow their lifecycle points; delivery errors are logged without changing the engine result.
- SHA resolution: the maintained action MUST check out one immutable PR HEAD
  SHA and supply it to the CLI. PR commands MUST fail when the live PR snapshot
  disagrees with that checkout identity.
- Apply and refresh MUST reuse one PR metadata snapshot for head identity, fork
  and draft policy, approvals, and gate evaluation. When the action supplies an
  immutable checkout identity, each command MUST revalidate the live head once
  after read-only gates and before lock, credential, or engine operations.
- Preview MUST reuse one PR metadata snapshot for head-SHA resolution and
  notification title and author fields within an invocation.
- Apply MUST bind its target stacks to the preview manifest for the resolved
  HEAD before a live changed-file result can classify the run as empty.

#### Scenario: PR head moves after checkout

- **GIVEN** the maintained action checked out an immutable PR head
- **WHEN** the PR head changes before a command binds its artifacts and gates
- **THEN** the command fails without using the newer head with the older tree

#### Scenario: PR head moves during gate evaluation

- **GIVEN** apply or refresh passed its initial checkout comparison
- **WHEN** the PR head changes before state-changing work begins
- **THEN** the command fails before acquiring a lock, workload credential, or invoking the engine

#### Scenario: Base movement changes the live file list

- **GIVEN** the preview manifest for the resolved HEAD contains a stack
- **AND** the live PR file list now reports every change outside the configured root
- **WHEN** apply resolves its target scope
- **THEN** it keeps the stack recorded by the preview manifest

#### Scenario: Independent projects overlap

- **GIVEN** at least two affected stacks in different project directories
- **AND** the preview parallelism limit is at least two
- **WHEN** preview executes
- **THEN** up to the configured number of engine previews run concurrently

#### Scenario: Workspaces sharing a directory stay serial

- **GIVEN** affected stacks share one project directory
- **WHEN** preview executes with a parallelism limit greater than one
- **THEN** engine previews for those stacks do not overlap
- **AND** their results remain in discovery order

#### Scenario: Preview publishes completion metadata

- **WHEN** preview needs the PR head SHA, title, and author
- **THEN** it reads the PR once and uses that coherent snapshot for the run
- Stacks declared with `path: .` (repo root) are triggered by any changed file
  that survives `ignore_changes` filtering.
- Docs/asset-only changes (skip globs) run nothing; preview/apply report
  "Documentation/asset-only changes".
- A preview with no target stacks MUST finish without acquiring state or
  workload credentials, opening the blob backend, or initializing an engine
  backend session.

#### Scenario: Documentation-only preview

- **WHEN** discovery maps the changed files to no stacks
- **THEN** preview reports success through its PR comment and CI result without
  blob access, notification dispatch, state authentication, or engine backend
  login
- Files mapping to no stack broaden to all stacks under `scope: auto` (default);
  `scope: pulumi_only` disables broadening. See discovery spec.

#### Scenario: GitHub reruns one workflow run

- **WHEN** attempts 1 and 2 use the same run number and commit SHA
- **THEN** each attempt receives a different run ID and cannot overwrite the
  other attempt's manifest or saved plans
- **AND** each attempt uses a different apply or refresh lock-holder identity
- **AND** attempt 2 cannot acquire an unexpired lease held by attempt 1
- **AND** attempt 2 can acquire the lock after the earlier lease expires

#### Scenario: Commits share a short SHA

- **GIVEN** two commits share the same seven-character SHA prefix
- **AND** preview history contains a legacy short-SHA manifest for the other
  commit
- **WHEN** apply, readiness, or explain loads the selected commit's preview
- **THEN** the legacy manifest for the other full commit is ignored
- **AND** new artifacts use the full commit SHA

#### Scenario: Invalid run attempt

- **GIVEN** a run attempt flag or environment value is present
- **AND** the value is nonnumeric, zero, or negative
- **WHEN** preview, apply, or refresh starts
- **THEN** the command fails before generating durable run identity

#### Scenario: Malformed preview history

- **GIVEN** preview history cannot be listed
- **OR** a candidate manifest for the selected commit cannot be read or decoded
- **OR** a candidate has an invalid timestamp, stack
  reference, or status
- **WHEN** apply selects the authoritative preview
- **THEN** apply fails closed and does not accept an older manifest
- **AND** it does not bypass the failure when the commit was already applied
- **WHEN** readiness reads the same history
- **THEN** it skips the success notification without failing the workflow
- **WHEN** explain reads the same history
- **THEN** it posts a diagnostic report with preview gates failed closed

#### Scenario: Break-glass recovers unavailable preview history

- **GIVEN** the selected commit's identity-bound preview history cannot be read or decoded
- **AND** break-glass is configured and authorizes the actor
- **WHEN** the actor requests apply with a mandatory justification
- **THEN** the unavailable preview gates are overridden as warnings
- **AND** apply uses only stacks matched precisely by the current changed-file mapping
- **AND** an unmapped file does not broaden recovery to every declared stack
- **AND** the PR comment, timeline, and audit record name the override
- **AND** checks, policy, locks, fork, and draft gates remain enforced

#### Scenario: Unrelated manifest is unavailable

- **GIVEN** an apply, refresh, different-commit, or unbound legacy manifest is
  unreadable or malformed
- **WHEN** apply, readiness, or explain selects the authoritative preview
- **THEN** the unrelated manifest is ignored

## Apply trigger modes

`apply.trigger` selects the apply-initiation path. It is a flow selector, not a
gate — it changes only *when* an apply starts, never *whether* the gates hold.

- `comment` (default) — apply-then-merge. Apply runs only from a `/reeve apply`
  (or `/reeve up`) comment. A merge event is a no-op.
- `merge` — merge-then-apply (continuous delivery). Apply runs when the PR is
  merged (`pull_request` `closed` with `merged: true`). A `/reeve apply` comment
  is a no-op.

Requirements:

- The binary is the source of truth for the mode. `run apply` receives
  `--trigger-source comment|merge` from the action and compares it against the
  configured `apply.trigger`; a mismatch is a deliberate no-op (exit success,
  nothing applied, one log line) so a mis-dispatched event cannot force an apply.
  Exactly one initiation path applies per repo.
- `merge` mode evaluates every gate against the PR HEAD SHA, identical to
  `comment` mode: approvals, checks-green, preview freshness/success, locks, and
  freeze all resolve exactly as pre-merge. `require_up_to_date` is the one gate
  whose result can differ post-merge (the base has advanced past HEAD); it
  fail-closes (blocks) and never opens, is off by default, and is intended for
  the apply-then-merge flow.
- Only a **merged** close dispatches an apply; a close without merge runs
  nothing. The already-applied guard dedups re-fires on the same commit.
- Break-glass is exempt from the trigger selector (explicit authorized
  emergency override); it still passes every other gate.
- An invalid `apply.trigger` value is rejected by config validation.

## Already-applied guard

A fully-clean apply (no failed/blocked stacks) writes `runs/pr-{n}/applied/{sha}.json`. A later run at the same commit:

- **apply** - skips work, posts the ⏭️ timeline notice, exits success.
- **preview** - renders the plan with an "already applied" banner.
- `--force` - bypasses the guard on both; re-runs all side effects.

## Out of scope (v1)

- Multiple engine configurations in one root. Separate roots in one repository may each configure an engine.
