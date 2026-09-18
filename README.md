<p align="center">
  <img src="docs/logo.svg" alt="Reeve logo" width="160" height="160">
</p>

# Reeve

**PR-native, self-hosted GitOps for Pulumi, Terraform, and OpenTofu.**
Preview infrastructure changes in a pull request, review the plan, and apply when your approval policy and checks pass.

**Reeve runs in your CI and stores nothing itself. Everything persistent lives in infrastructure you control.**

- **Not SaaS.** Reeve is designed to run entirely in infrastructure you control.
- **No phone-home.** Optional notifications and telemetry go to destinations you configure.
- **No Reeve account.** Use your existing GitHub and cloud identities.
- **MIT licensed.** The project is committed to remaining permissively licensed.

## See it work

![Reeve preview showing changes for two OpenTofu stacks](docs/images/preview.jpg)

A real preview from the [reeve-test playground](https://github.com/reeveops/reeve-test).
[Screenshot source and version](docs/images/README.md).

1. Open or update a PR. Reeve previews affected stacks and posts the results in GitHub.
2. Review the plan and obtain the required approvals.
3. Comment `/reeve apply`. Reeve evaluates the gates, takes per-stack locks, and applies the changes.

The PR shows changes, gate failures, and apply results; locks, artifacts, and audit entries live in your bucket.
[How the PR workflow works](docs/pull-requests.md).

## Get started

| I want to… | Start here |
| --- | --- |
| Try Reeve locally without cloud resources | [Local demos](examples/README.md#local-demos) |
| Follow a guided PR tour without local setup | [Guided playground](examples/README.md#guided-pr-tour) — currently requires write access to `reeve-test`. |
| Connect an existing infrastructure repository | [Getting started](docs/getting-started.md) |
| Explore deeper scenarios and E2E tests | [Test harness and scenarios](examples/README.md#test-harness-and-scenarios) |

For GitHub Actions you need an infrastructure project, a persistent Reeve bucket, and credentials for the bucket, IaC backend, and workload.
Reeve uses your engine's existing state backend; it does not replace it.

Run `reeve init` in your infrastructure repository to scaffold configuration and, when an exact source commit is available, the workflow caller.
The [setup guide](docs/getting-started.md) covers installation, engine selection, storage, authentication, and your first PR.

The maintained [shared workflow](docs/github-actions.md) handles event routing, checkout, binary setup, engine installation, and preview concurrency.
Use the composite action when you need custom preparation or a platform the shared workflow does not support.

## Status

**Beta, entering the release candidate phase.** Beta testers are actively using Reeve in production for Pulumi and Terraform workflows.

Configuration and behavior may change before 1.0; review release notes and use `reeve migrate-config` when a schema changes.
[Releases](https://github.com/reeveops/reeve/releases) provide binaries; the [version guide](docs/github-actions.md#versions-and-binaries) explains stable releases and exact-commit candidates.

## Add what you need

| Capability | Guide |
| --- | --- |
| Approvals, CODEOWNERS, checks, freshness, and freeze windows | [PR workflow](docs/pull-requests.md) |
| Cloud federation, local credentials, and secret managers | [Authentication](docs/auth.md) |
| Scheduled drift checks and alerts | [Drift detection](docs/drift.md) |
| Slack, webhooks, PagerDuty, and deployment timelines | [Notifications](docs/notifications.md) |
| Policy checks before apply | [Policy hooks](docs/policy-hooks.md) |
| Emergency apply with recorded justification | [Break-glass](docs/break-glass.md) |
| Storage, maintenance, troubleshooting, and upgrades | [Self-hosting](docs/self-hosting.md) · [Operations](docs/operations.md) |
| Exact configuration fields and defaults | [Configuration reference](docs/configuration.md) |

[Browse all documentation](docs/README.md).
The evolving [reeve-test](https://github.com/reeveops/reeve-test) harness supplies runnable scenarios and acceptance evidence; its README describes current coverage and limitations.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and checks, and [OpenSpec](openspec/README.md) for implementation contracts and proposals.

## License

[MIT](LICENSE).
