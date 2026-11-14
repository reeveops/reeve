# CORE — plan locking, refresh runner, freshness default delta

## ADDED Requirements

### Requirement: Apply executes the plan the PR previewed

When `engine.plan_locking` is enabled (the default) and the engine supports
saved plans, preview SHALL store each stack's plan artifact under the run's own
prefix and record its key AND SHA-256 digest on the stack's manifest entry. If
storing or uploading the artifact fails at preview time, the manifest SHALL
record NO plan key for that stack and the preview timeline SHALL carry the
reason - a preview that cannot deliver its artifact keeps no promise about
one. Apply SHALL fetch the recorded artifact for the commit being applied and
hand it to the engine.

Degradation SHALL be one-directional: the ONLY apply-time fallback is a
manifest entry with no plan key, which re-plans with a loud timeline
statement. A manifest entry that DOES record a plan key keeps a promise - a
missing, unreadable, oversized, digest-less, or digest-mismatched artifact
MUST NOT fall back to a fresh plan; it fails that stack closed per the next
requirement. Neither direction SHALL be silent.

#### Scenario: The reviewed plan is applied

- **WHEN** a preview stored a plan for a stack and that commit is applied
- **THEN** the engine receives that exact artifact

#### Scenario: The preview's upload failure records no plan key

- **WHEN** the preview cannot store or upload a stack's plan artifact
- **THEN** the manifest records no plan key for that stack
- **AND** the preview timeline carries an entry naming the stack and the
  failure, so the run's record shows locking was unavailable, not silently
  absent

#### Scenario: No artifact exists

- **WHEN** the manifest for the commit records no plan key (locking was off,
  the manifest predates the feature, or the preview's upload failed and
  recorded no key)
- **THEN** the stack applies with a fresh plan, and the timeline carries a
  "plan lock unavailable" entry naming the stack and the reason

### Requirement: The saved plan is digest-bound and its absence fails closed

The manifest entry for a stored plan SHALL record the artifact's SHA-256
digest. When a manifest records a plan key but the artifact is missing,
unreadable, oversized (beyond the 64 MiB plan-artifact cap
`maxPlanArtifactBytes` in `internal/run/plan_artifacts.go`), digest-less, or
does not match the recorded digest, apply SHALL fail that stack closed
instead of computing a fresh plan - including a key recorded with NO digest,
which manifests written by previews predating digest binding carry. Fresh-plan
re-planning is an explicit recovery operation: an
`engine.plan_locking.allow_replan` configuration setting (default `false`,
deny) and a per-run `--replan` flag - on the CLI and on `/reeve apply` in a
PR comment (the comment parser's accepted argument set for `apply` gains it),
each behind the same actor gates as apply - allow it, and a re-plan SHALL be
stated on the timeline and in the audit record with the actor. A manifest
with NO plan key remains the expressly supported compatibility path
(manifests predating plan locking, plan locking disabled or the upload
failing at preview time) and re-plans with the loud timeline statement.

#### Scenario: A manifest promises a plan that is missing

- **WHEN** the manifest records a plan key and the artifact cannot be
  fetched
- **THEN** the apply fails that stack, naming the stack, the run id, the
  plan key, and the failure
- **AND** no engine runs for that stack without the explicit recovery path

#### Scenario: A manifest records a key but no digest

- **WHEN** the manifest records a plan key but no digest - a manifest written
  by a preview that predates digest binding, which the current beta writes
- **THEN** the apply fails that stack closed, naming the stack, the run id,
  and the reason: the preview predates digest binding, so the artifact the
  key names cannot be verified
- **AND** the refusal names the next step - run a new preview, so the
  manifest carries a digest - and the re-planning recovery path below
  applies to it exactly as to any other unkept promise

#### Scenario: The artifact does not match its digest

- **WHEN** the fetched plan artifact's SHA-256 differs from the digest the
  manifest recorded
- **THEN** the apply fails that stack, naming the stack, the plan key, and
  both digests
- **AND** the mismatched artifact is treated as corrupt, never executed

#### Scenario: The recovery path from the CLI

- **GIVEN** `engine.plan_locking.allow_replan: true` in trusted
  configuration, or `--replan` passed to `reeve apply` on the CLI
- **WHEN** the promised plan is unavailable
- **THEN** the apply computes a fresh plan
- **AND** the timeline and the audit record state that the apply re-planned
  through the recovery path, naming the run's actor

#### Scenario: The recovery path from a PR comment

- **GIVEN** the comment parser's accepted argument set for `apply` includes
  `--replan`
- **WHEN** an authorized commenter runs `/reeve apply --replan` - behind the
  same actor gates as any apply (the bot guard, the association gate, and
  the explicit-command actor gate)
- **THEN** the apply computes a fresh plan through the recovery path
- **AND** the timeline and the audit record state it, naming the commenting
  actor
- **AND** a commenter who fails apply's actor gates re-plans nothing

#### Scenario: Legacy manifest without a plan key

- **WHEN** the manifest for the commit records no plan key
- **THEN** the stack applies with a fresh plan under the compatibility path
- **AND** the timeline carries the "plan lock unavailable" entry naming the
  stack and the reason

#### Scenario: The artifact is scoped to the run

- **WHEN** a plan artifact is stored
- **THEN** its key is under `runs/pr-<n>/<run-id>/plans/`, regardless of what
  characters the stack name contains, so the existing age-based retention
  sweep prunes it with the run that produced it

#### Scenario: The runner-local copy does not outlive the apply

- **WHEN** an apply fetches a plan artifact
- **THEN** the temp file is created 0600 and removed as soon as the engine
  returns, not at the end of the run

### Requirement: Refresh is a PR-level operation

`/reeve refresh` SHALL reconcile engine state with live infrastructure for the
stacks in scope, defaulting to the stacks this PR's changed files map to and
covering every declared stack with `--all`. `--dry-run` SHALL report without
writing state and SHALL take no locks, because it is a read.

Refresh SHALL enforce the fork-PR policy, the draft-PR gate, freeze windows,
and per-stack locks. It SHALL NOT enforce approvals, required checks, or
preview freshness: those gate a change set, and a refresh has none.

A writing refresh SHALL be recorded in the audit log.

#### Scenario: Scope does not broaden

- **WHEN** a changed file maps to no specific stack
- **THEN** the refresh covers the precise matches only, never every declared
  stack

#### Scenario: A locked stack

- **WHEN** another PR holds a stack's lock
- **THEN** that stack is reported blocked rather than refreshed: a state
  rewrite must not race an apply

#### Scenario: Counts are not infrastructure change

- **WHEN** the refresh comment is rendered
- **THEN** it states that counts are state reconciliation, so a "delete" is not
  read as reeve having destroyed a resource

### Requirement: --refresh turns plan locking off for that run

`/reeve apply --refresh` SHALL reconcile state before applying, and SHALL
therefore not use a locked plan. The run timeline SHALL say so.

#### Scenario: Refresh requested with a stored plan available

- **WHEN** an apply runs with `--refresh` and a plan artifact exists for the
  commit
- **THEN** the artifact is not used, and the timeline records that the applied
  change set was computed after the refresh and is not the plan the PR
  previewed

## MODIFIED Requirements

### Requirement: Preview freshness bounds how stale an applied plan may be

`preconditions.preview_freshness` SHALL default to 4 hours when the key is
omitted. Only a literal `"0"` SHALL disable the gate. A value that is not a
positive Go duration SHALL be a load error, never a silent disable.

#### Scenario: No configuration

- **WHEN** `preview_freshness` is not set
- **THEN** the gate is active with a 4h window

#### Scenario: Deliberate opt-out

- **WHEN** `preview_freshness: "0"`
- **THEN** the gate is skipped, and its trace says what is no longer checked
