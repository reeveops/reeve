# Examples and test scenarios

Choose a local demo to see Reeve work, a wiring recipe to connect your services, or a test scenario to explore an edge case.
Recipes contain placeholders and do not all include workload projects or a workflow.

## Local demos

| Demo | Requires | Expected result |
| --- | --- | --- |
| [Pulumi](toy-stack/README.md) | Reeve, Pulumi, Node.js/npm, Python 3; network for dependencies. | Preview three random-provider stacks with local state and no cloud workload credentials. |
| [Terraform / OpenTofu](toy-stack-terraform/README.md) | Reeve and the selected engine; network for provider initialization. | Preview two root modules and three declared workspaces using local state. |
| [Policy checks](policy-opa/README.md) | Conftest 0.70.0, Python 3. | Allowed and denied synthetic inputs demonstrate blocking and advisory policies. |

Follow each demo's setup and cleanup instructions in a disposable checkout.
Local files persisting within a demo are not shared storage for separate hosted CI runs.

## Guided PR tour

The [reeve-test guided playground](https://github.com/reeveops/reeve-test/blob/master/docs/playground.md) creates a temporary PR and walks you through Reeve in GitHub, without local installation or cloud workload credentials.
It currently requires write access to the repository; other readers can use the local demos above or inspect a [completed OpenTofu tour](https://github.com/reeveops/reeve-test/pull/33).

Choose OpenTofu, Terraform, or Pulumi through the playground guide and follow the progress comment.
The tour demonstrates previews with additions, changes, deletions, and replacements; approval denial and apply; repeated apply; requested changes; lock blocking; engine failure; and break-glass recovery.
Only the requesting user advances the tour. Use `/playground finish` to stop, and check the workflow result if a session ends unexpectedly.

The controller polls human comments and invokes Reeve against trusted fixtures, with real GitHub App reviews and local state kept in one job.
The ordinary shared GitOps workflow skips playground PRs, so this demonstrates the guided interaction rather than the standard workflow's comment routing or persistence across runners.
Public admission and broader harness coverage are still being developed.

## Integration recipes

| Recipe | Includes | You supply |
| --- | --- | --- |
| [AWS OIDC](aws-oidc/README.md) | Pulumi auth/config and shared-workflow caller. | Workload projects, roles/backend, persistent bucket, and a prepared runner identity for bucket access. |
| [GCP WIF](gcp-wif/README.md) | Federation setup, Pulumi config, GCP-authenticated caller. | Workload projects, project/service accounts, state backend, bucket, and repository variables. |
| [Multi-cloud](multi-cloud/README.md) | AWS/GCP and secret-manager bindings, mode-specific roles. | Workload, controller retrieval credentials, backend, and workflow. |
| [Scheduled drift](drift-scheduled/README.md) | Drift configuration and named-schedule caller. | Existing authenticated workload, persistent baseline, destination credentials, and prepared runner. |

Run `reeve lint` after adapting a recipe and confirm `reeve stacks` lists the intended projects/stacks.
A syntactically valid selector can match nothing; references use `project/stack`, such as `api/prod` and `*/prod`.

## Test harness and scenarios

[reeve-test](https://github.com/reeveops/reeve-test) is the evolving public test harness and playground.
It now includes local regression tests, a guided PR tour, live GitHub identity tests, and storage-adapter checks; its current README, workflows, and run results define each lane's coverage.

| Question | Current entry point | Boundary |
| --- | --- | --- |
| How do create/update/delete/no-op and saved plans behave? | [Local E2E coverage](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md#coverage) | Real engines, simulated GitHub, disposable state; cases vary by engine. |
| What blocks approval or a changed commit? | [Authorization matrices](https://github.com/reeveops/reeve-test/blob/master/e2e/authorization_matrix_test.go), [live identity setup](https://github.com/reeveops/reeve-test/blob/master/e2e/github-apps.md) | CODEOWNERS, explicit and combined policies, public-review opt-in, and break-glass allow/deny cases; Reeve approval evaluation is distinct from GitHub native merge eligibility. |
| What happens with concurrent previews, lock queues, cancellation, or maintenance? | [E2E scenario source](https://github.com/reeveops/reeve-test/tree/master/e2e) and its coverage table | Process-level scenarios, not a promise that every cloud backend passed. |
| How do retries and immutable commit identities work? | [E2E coverage and reports](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md) | Check the scenario and tested source pin against the behavior you need. |
| How do separate engine roots call the workflow? | [Repository layout](https://github.com/reeveops/reeve-test#opentofu--terraform-scenarios-tf) | The demo callers' filesystem state is job-local. |
| Has a storage backend been exercised? | [Local adapter coverage](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md#coverage), [cloud contracts](https://github.com/reeveops/reeve-test/blob/master/e2e/cloud-buckets.md) | Emulator runs include expected conditional-delete limitations; real AWS/GCP acceptance needs configured resources and a recorded result. |

Use the maintained page to find a scenario, and record immutable Reeve/fixture commits when citing its result.
A workflow pinned to an older candidate, an emulator check, or a screenshot is not acceptance of every later master commit or a full production deployment.

Recorded results against Reeve `d31c814640689c2f2e1b0d02d2bc11a80a94faab` include a [local regression run](https://github.com/reeveops/reeve-test/actions/runs/35300131937) at harness commit `eae46d2` and a [completed guided OpenTofu run](https://github.com/reeveops/reeve-test/actions/runs/35300745492) at `ebae40d`.
The local run checks showcase output for all three engines; the recorded guided run establishes the full live tour for OpenTofu.

Human commands now drive the guided tour. Its controller and the separate App acceptance harness still use one-job filesystem state, so neither proves the standard event-driven workflow across separate runners.
Policy integrations, notification destinations, and Azure/R2 acceptance should only be described as covered when corresponding scenarios and results exist.

## Keeping examples useful

A runnable scenario should state prerequisites, setup/invocation, expected output (including expected blocks), cleanup, and tested tool versions.
Keep the setup summary in the guide and the deeper scenario beside the executable fixture.

The documentation checks validate local links, workflow inputs, configuration, and representative selector/binding behavior.
Run the policy check and local demos separately for execution evidence; cloud wiring recipes still require your actual service setup.
