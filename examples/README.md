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
It is being expanded into a stronger harness and E2E suite; its current README, workflows, and run results define what is available today.

| Question | Current entry point | Boundary |
| --- | --- | --- |
| How do create/update/delete/no-op and saved plans behave? | [Local E2E coverage](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md#coverage) | Real engines, simulated GitHub, disposable state; cases vary by engine. |
| What blocks approval or a changed commit? | [Local gates](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md#coverage), [live identity setup](https://github.com/reeveops/reeve-test/blob/master/e2e/github-apps.md) | Reeve approval evaluation is distinct from GitHub native merge eligibility. |
| What happens with concurrent previews, lock queues, cancellation, or maintenance? | [E2E scenario source](https://github.com/reeveops/reeve-test/tree/master/e2e) and its coverage table | Process-level scenarios, not a promise that every cloud backend passed. |
| How do retries and immutable commit identities work? | [E2E coverage and reports](https://github.com/reeveops/reeve-test/blob/master/e2e/README.md) | Check the scenario and tested source pin against the behavior you need. |
| How do separate engine roots call the workflow? | [Repository layout](https://github.com/reeveops/reeve-test#opentofu--terraform-scenarios-tf) | The demo callers' filesystem state is job-local. |
| Has a storage backend been exercised? | [Cloud contracts](https://github.com/reeveops/reeve-test/blob/master/e2e/cloud-buckets.md), [bootstrap](https://github.com/reeveops/reeve-test/blob/master/e2e/bootstrap/README.md) | Real AWS/GCP lanes are opt-in; inspect configuration and a recorded result. |

Use the maintained page to find a scenario, and record immutable Reeve/fixture commits when citing its result.
A workflow pinned to an older candidate, an emulator check, or a screenshot is not acceptance of every later master commit or a full production deployment.

The existing live App harness does not establish a successful human slash-command path, and its one-job filesystem cannot prove persistence across separate workflow runs.
Policy integrations, notification destinations, and Azure/R2 acceptance should only be described as covered when corresponding scenarios and results exist.

## Keeping examples useful

A runnable scenario should state prerequisites, setup/invocation, expected output (including expected blocks), cleanup, and tested tool versions.
Keep the setup summary in the guide and the deeper scenario beside the executable fixture.

The documentation checks validate local links, workflow inputs, configuration, and representative selector/binding behavior.
Run the policy check and local demos separately for execution evidence; cloud wiring recipes still require your actual service setup.
