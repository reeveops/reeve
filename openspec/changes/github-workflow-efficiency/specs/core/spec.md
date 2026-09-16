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

The public reusable workflow MUST require `gitops`, `drift`, or `maintenance` mode.
It MUST invoke the composite action from the same Reeve commit as the workflow file.

#### Scenario: GitOps mode

- GIVEN a caller selects `gitops` mode with PR and comment triggers
- WHEN an accepted event starts the called workflow
- THEN it MUST provide routing, checkout, setup, timeout, and preview concurrency policy.

#### Scenario: Default comment prefilter

- GIVEN `gitops` mode uses the default `/reeve` command prefix
- WHEN a comment does not begin with that prefix and a following space
- THEN the reusable workflow job MUST skip before a runner is assigned.

#### Scenario: Multiple shared-workflow prefixes

- GIVEN a GitOps caller supplies more than one comma-separated command prefix
- WHEN the reusable workflow validates its inputs
- THEN it MUST reject the configuration instead of assigning runners to unrelated comments.

#### Scenario: Merge queue required check

- GIVEN a consumer configures `reeve / Reeve` as a required check
- WHEN GitHub requests checks for a merge group
- THEN the generated caller MUST publish the stable skipped result without credentialed workload execution.

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

#### Scenario: Maintenance mode

- GIVEN a scheduled or manual caller selects `maintenance` mode
- WHEN the called workflow starts
- THEN it MUST run `reeve maintenance run` without installing an IaC engine.

#### Scenario: Conflicting drift scope

- GIVEN a caller selects both a named schedule and a stack pattern
- WHEN the action validates its typed drift inputs
- THEN it MUST stop before Reeve execution with a clear error.

#### Scenario: Named integration secrets

- GIVEN the caller needs a Reeve token override or notification token
- WHEN it invokes the reusable workflow
- THEN it MUST map only the named secret and MUST NOT require `secrets: inherit`.

#### Scenario: Engine credentials

- GIVEN an engine or state backend requires credentials
- WHEN the reusable workflow invokes Reeve
- THEN it MUST resolve them through configured auth providers and MUST NOT accept long-lived engine credentials as workflow-call secrets.

#### Scenario: Engine setup

- GIVEN a caller selects a Pulumi, OpenTofu, or Terraform CLI version
- WHEN the reusable workflow accepts an event
- THEN it MUST install the selected CLI after event classification and disable wrapper shims for HCL engines.

#### Scenario: Ineligible prebuilt binary

- GIVEN the action source is an unsupported ref, operating system, or architecture
- WHEN the binary cache misses
- THEN it MUST skip verifier installation and build the exact checked source.

#### Scenario: Signed prerelease tag

- GIVEN the action source is a semantic-version prerelease tag
- WHEN the binary cache misses
- THEN it MUST resolve and verify that tag's release artifact before falling back to source.

### Requirement: Workload checkout uses an immutable revision

The maintained action MUST resolve one full workload commit SHA before checkout.
It MUST verify the checkout and current PR head before authentication, engine setup, and Reeve execution.

#### Scenario: Pull request event

- GIVEN a pull request or review event carries a full head SHA
- WHEN the action prepares the workload
- THEN it MUST checkout that exact SHA instead of a moving pull request ref.

#### Scenario: Pull request comment

- GIVEN an accepted issue comment targets a pull request
- WHEN the action resolves the workload
- THEN it MUST read the current full head SHA through the GitHub API before checkout.

#### Scenario: Head moves before execution

- GIVEN the action checked out an immutable PR head
- WHEN the live PR head differs before setup or command execution
- THEN it MUST fail without authenticating or invoking Reeve against a mixed revision.

### Requirement: Pinned workflow scaffolding

`reeve init` MUST generate a GitOps caller when it has an exact Reeve source commit.
It MUST preserve an existing workflow and MUST reject a moving workflow ref.

#### Scenario: Release initialization

- GIVEN a release binary embeds its full source commit
- WHEN a user runs `reeve init` in a repository without a Reeve workflow
- THEN it MUST write a caller pinned to that commit with the detected engine setup, baseline permissions, and comment-trigger events only.

#### Scenario: Existing federated configuration

- GIVEN initialization preserves config containing a federated auth provider
- WHEN it generates a missing caller workflow
- THEN the caller MUST grant `id-token: write`.

#### Scenario: Existing merge-trigger configuration

- GIVEN initialization preserves config with `apply.trigger: merge`
- WHEN it generates a missing caller workflow
- THEN the caller MUST subscribe to merged pull request events.

#### Scenario: Existing workflow

- GIVEN `.github/workflows/reeve.yml` already exists
- WHEN a user runs `reeve init` with or without `--force`
- THEN it MUST preserve the existing workflow.

#### Scenario: Development initialization

- GIVEN a development binary does not embed a full source commit
- WHEN a user supplies `--workflow-ref`
- THEN Reeve MUST require a full 40-character commit SHA before writing any generated files.
