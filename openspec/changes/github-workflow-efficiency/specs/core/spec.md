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
- WHEN a comment without that prefix triggers the caller workflow
- THEN the reusable workflow job MUST skip before a runner is assigned.

#### Scenario: Drift mode

- GIVEN a scheduled or manual caller selects `drift` mode
- WHEN the called workflow starts
- THEN it MUST run `reeve drift run` without PR write permission.

#### Scenario: Named secrets

- GIVEN the caller needs a Reeve token override, notification token, or engine credential
- WHEN it invokes the reusable workflow
- THEN it MUST map only the named secret and MUST NOT require `secrets: inherit`.

#### Scenario: Engine setup

- GIVEN a caller selects a Pulumi, OpenTofu, or Terraform CLI version
- WHEN the reusable workflow accepts an event
- THEN it MUST install the selected CLI after event classification and disable wrapper shims for HCL engines.
