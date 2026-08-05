# CORE — plan locking, refresh runner, freshness default delta

## ADDED Requirements

### Requirement: Apply executes the plan the PR previewed

When `engine.plan_locking` is enabled (the default) and the engine supports
saved plans, preview SHALL store each stack's plan artifact under the run's own
prefix and record its key on the stack's manifest entry. Apply SHALL fetch that
artifact for the commit being applied and hand it to the engine.

Degradation SHALL be one-directional: a missing or unreadable artifact falls
back to computing a plan at apply time, and SHALL be reported on the run
timeline. It SHALL NOT be silent.

#### Scenario: The reviewed plan is applied

- **WHEN** a preview stored a plan for a stack and that commit is applied
- **THEN** the engine receives that exact artifact

#### Scenario: No artifact exists

- **WHEN** the manifest for the commit records no plan key (locking was off,
  the upload failed, or the manifest predates the feature)
- **THEN** the stack still applies, and the timeline carries a
  "plan lock unavailable" entry naming the stack and the reason

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
