## ADDED Requirements

### Requirement: Event classification precedes action setup

The composite action MUST classify an event before binary, checkout, authentication, or engine setup.
Every later action step MUST depend on an accepted classification.

#### Scenario: Ordinary comment

- GIVEN an issue comment whose first line is not an accepted Reeve command
- WHEN the composite action starts
- THEN it MUST skip binary, checkout, authentication, engine, and Reeve execution steps.

#### Scenario: Rejected command

- GIVEN a bot, unauthorized author, plain issue, unknown verb, or malformed command
- WHEN the composite action classifies the event
- THEN it MUST perform no later setup or command execution.

#### Scenario: Accepted command

- GIVEN an authorized pull request comment with an accepted prefix and verb
- WHEN the composite action classifies the event
- THEN it MUST preserve the command and structured arguments for execution.

#### Scenario: Explicit trusted command

- GIVEN the action receives an explicit single-line command input
- WHEN the event would not otherwise dispatch Reeve
- THEN it MUST execute the explicit command through the same guarded setup path.

### Requirement: Shared workflow modes

The public reusable workflow MUST require either `gitops` or `drift` mode.
It MUST invoke the composite action from the same Reeve commit as the workflow file.

#### Scenario: GitOps mode

- GIVEN a caller selects `gitops` mode with PR and comment triggers
- WHEN an accepted event starts the called workflow
- THEN it MUST provide routing, checkout, setup, timeout, and preview concurrency policy.

#### Scenario: Default comment prefilter

- GIVEN `gitops` mode uses the default `/reeve` command prefix
- WHEN a comment does not begin with that prefix and a following space
- THEN the reusable workflow job MUST skip before a runner is assigned.

#### Scenario: Quoted command

- GIVEN an ordinary comment contains `/reeve` after prose or quote markup
- WHEN the comment triggers the caller workflow
- THEN the reusable workflow job MUST skip before a runner is assigned.

#### Scenario: Drift mode

- GIVEN a scheduled or manual caller selects `drift` mode
- WHEN the called workflow starts
- THEN it MUST run `reeve drift run` without requesting PR write permission.

#### Scenario: Scoped drift mode

- GIVEN a drift caller selects a named schedule or stack pattern and optional stale-only filtering
- WHEN the called workflow starts
- THEN it MUST pass each value as a distinct CLI argument without shell evaluation.

#### Scenario: Conflicting drift scope

- GIVEN a caller selects both a named schedule and a stack pattern
- WHEN the action validates its typed drift inputs
- THEN it MUST stop before Reeve execution with a clear error.

#### Scenario: Named secrets

- GIVEN the caller needs a Reeve token override, notification token, or engine credential
- WHEN it invokes the reusable workflow
- THEN it MUST map only the named secret and MUST NOT require `secrets: inherit`.

#### Scenario: Engine setup

- GIVEN a caller selects a Pulumi, OpenTofu, or Terraform CLI version
- WHEN the reusable workflow accepts an event
- THEN it MUST install the selected CLI after event classification and disable wrapper shims for HCL engines.

### Requirement: Pinned workflow scaffolding

`reeve init` MUST generate a GitOps caller when it has an exact Reeve source commit.
It MUST preserve an existing workflow and MUST reject a moving workflow ref.

#### Scenario: Release initialization

- GIVEN a release binary embeds its full source commit
- WHEN a user runs `reeve init` in a repository without a Reeve workflow
- THEN it MUST write a caller pinned to that commit with the detected engine setup, baseline permissions, and comment-trigger events only.

#### Scenario: Existing workflow

- GIVEN `.github/workflows/reeve.yml` already exists
- WHEN a user runs `reeve init` with or without `--force`
- THEN it MUST preserve the existing workflow.

#### Scenario: Development initialization

- GIVEN a development binary does not embed a full source commit
- WHEN a user supplies `--workflow-ref`
- THEN Reeve MUST require a full 40-character commit SHA before writing any generated files.
