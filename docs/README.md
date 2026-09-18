# Reeve documentation

Start with the task you want to complete. Optional integrations and detailed reference material are available when you need them.

## Start here

- [Getting started](getting-started.md): connect an existing repository and complete your first preview, review, and apply.
- [Local demos](../examples/README.md#local-demos): try Pulumi or Terraform/OpenTofu without cloud workload credentials.
- [Guided PR tour](../examples/README.md#guided-pr-tour): follow an interactive playground in GitHub; currently requires write access to `reeve-test`.
- [PR workflow](pull-requests.md): read results, approve changes, apply, and understand a blocked run.

## Set up your environment

| Guide | Use it for |
| --- | --- |
| [GitHub Actions](github-actions.md) | Shared workflow, inputs, permissions, version pins, nested roots, and custom action setup. |
| [Self-hosting](self-hosting.md) | Persistent storage, IaC state, runners, retention, and the services you operate. |
| [Authentication](auth.md) | GitHub, bucket, backend, and workload identities; provider recipes and bindings. |

## Add capabilities

| Guide | Use it for |
| --- | --- |
| [Drift detection](drift.md) | Establish a baseline, schedule checks, and tune drift alerts. |
| [Notifications](notifications.md) | Slack, webhooks, PagerDuty, issues, and deployment timelines. |
| [Policy hooks](policy-hooks.md) | Run a policy command against the plan before apply. |
| [Break-glass](break-glass.md) | Configure and use an audited emergency override. |

## Look something up

- [Configuration reference](configuration.md): fields, defaults, constraints, and configuration migration.
- [Operations](operations.md): troubleshoot runs, inspect artifacts, maintain locks, and upgrade.
- [Examples and test scenarios](../examples/README.md): minimal demos, integration recipes, and the evolving `reeve-test` harness.

## Contribute

[CONTRIBUTING.md](../CONTRIBUTING.md) covers development and checks.
[OpenSpec](../openspec/README.md) contains current implementation contracts and separate change proposals; users do not need to read specs to set up Reeve.
