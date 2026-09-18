# Pulumi policy hooks

The original Pulumi policy example lives here.
It covers required tags, allowed regions, an advisory cost hook, production deletion checks, and an advisory change-count limit.
[Terraform](../policy-opa-terraform/README.md) and [OpenTofu](../policy-opa-opentofu/README.md) have separate configurations and fixtures.

## Run the local checks

Install [Conftest](https://www.conftest.dev/install/) `0.70.0` and Python 3, then run from the repository root:

```bash
bash examples/policy-opa/check.sh
```

The allowed fixture passes. Missing, empty, or unknown tags, a second untagged resource, disallowed or missing regions, production deletes, and malformed engine-plan shapes fail.
The change-limit script accepts the small fixture and rejects the large fixture; Reeve reports that hook as advisory.
The fixtures are synthetic: no cloud resources, pricing calls, or persistent state are created, so no cleanup is needed.

## What to copy

| File | Purpose |
| --- | --- |
| [`.reeve/pulumi.yaml`](.reeve/pulumi.yaml) | `engine.type: pulumi`, project/stack declarations, and hook commands. |
| [`.reeve/shared.yaml`](.reeve/shared.yaml) | Placeholder bucket, approvals, and apply settings to adapt. |
| [`policies/require_tags.rego`](policies/require_tags.rego) | Require nonempty `cost-center` and `owner` tags in production. |
| [`policies/restrict_regions.rego`](policies/restrict_regions.rego) | Allow `us-east-1`, `us-east-2`, and `us-west-2` for the declared production region. |
| [`policies/prevent_deletes.rego`](policies/prevent_deletes.rego) | Block production deletions using Reeve's engine-independent counts. |
| [`scripts/cost-gate.sh`](scripts/cost-gate.sh) | Original advisory cost integration sketch; see the setup limits below. |
| [`scripts/change-limit.py`](scripts/change-limit.py) | Warn when the aggregate change count exceeds the command's limit. |

Copy the recipe into a root for this engine, adapt the placeholders, and add your workload projects.
Reeve loads one engine configuration per root; keep the other engines in their own roots.
Run Reeve from the directory containing `policies/` and `scripts/`; `--root` does not change the process's working directory.

## What these rules inspect

The tag rule reads Pulumi `plan.steps[].newState.inputs.tags` on `aws:ec2/instance:Instance` resources with a proposed new state.
It rejects a missing engine-plan array and checks every matching resource, rather than searching the human summary for a tag name.
Extend it with fixtures for other resource types, provider defaults, and engine-specific representations of unknown values before relying on those cases.

The region rule checks Pulumi `plan.config["aws:region"]`.
It validates the declared default region, not every resource's effective region: explicit providers, provider aliases, module providers, and per-resource overrides need additional rules.
Missing or unknown declared regions fail in production.

## Cost hook

The original [`scripts/cost-gate.sh`](scripts/cost-gate.sh) is retained as an Infracost integration sketch.
It demonstrates advisory handling (`on_fail: warn`), skipping when Infracost is absent, and monthly-delta caps of $1,000 for production, $300 for staging, and $100 elsewhere.
Its `MAX_MONTHLY_DELTA_USD` override works when invoking the script directly; Reeve does not pass that arbitrary environment variable through to hooks.

This sketch still needs an engine-specific input adapter and authenticated execution setup before use:

- Reeve supplies its wrapper, while a Terraform JSON consumer needs the nested `.plan` object. Pulumi preview JSON has a different schema and needs a supported estimator or conversion.
- The script expects `projects[].diff.totalMonthlyCost` from Infracost; validate that field against the estimator command and version you select. A missing estimate must not be mistaken for zero cost.
- Hooks do not inherit an ambient `INFRACOST_API_KEY`, and CI hooks use temporary HOME/XDG directories. Installing Infracost or authenticating another workflow step alone does not establish hook authentication.

The script is preserved so the cost-policy example remains available. The local check validates its shell syntax only; it does not claim a working pricing integration.
See [Infracost's plan JSON input](https://www.infracost.io/docs/integrations/infracost_api/) and [Reeve's hook environment](../../docs/policy-hooks.md#apply-ordering-and-credentials) when adapting it.

## CI and apply behavior

Install Conftest and Python on a prepared runner or in a [custom composite-action job](../../docs/github-actions.md#composite-action-for-custom-jobs).
The shared workflow does not have an arbitrary setup-step input.

On apply, Reeve writes each stack's preview wrapper to a temporary file and runs the configured hooks after the independent apply gates pass.
Tag, region, and deletion failures block that stack's apply; cost and change-count failures are warnings.
[Policy hooks](../../docs/policy-hooks.md) documents the wrapper, command environment, defaults, and gate ordering.

Run all three engines' fixture checks from the repository root with `mise run policy-example`.
