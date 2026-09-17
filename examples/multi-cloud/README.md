# Multi-cloud binding recipe

Bind AWS and GCP identities to the same production stack, replace the AWS workload role for `payments/prod`, and use separate drift bindings.
This is an advanced configuration recipe without workload projects or a workflow; start with the [AWS](../aws-oidc/README.md) and [GCP](../gcp-wif/README.md) setup first.

## Prerequisites

Prepare Reeve-bucket access, a Pulumi backend, cloud federation roles, and the external-service secrets shown in the configuration.
The AWS secret-manager and SSM lookups use the controller's AWS SDK identity; `source:` does not wire a federated parent provider into retrieval.

## Binding behavior

- `*/prod` matches production stacks across projects.
- Preview and apply receive the configured AWS/GCP roles and external-service tokens.
- `payments/prod` replaces only the AWS workload role in preview/apply.
- Drift receives the explicitly listed read-only AWS/GCP roles and does not inherit the service-token providers.

The example configures state authentication separately, but stack credentials override identical environment keys during engine execution.
Ensure each effective engine identity can access the state backend; the example does not establish two independent AWS identities within one subprocess.

## Validate

Copy the configuration into a consumer root with real stack declarations, replace the placeholders, and run `reeve lint` and `reeve stacks`.
Use `reeve rules explain payments/prod` to inspect approval policy, then preview a small change using the intended bindings.

This recipe provisions nothing by itself and proves no cloud acceptance result.
The [scenario catalog](../README.md#test-harness-and-scenarios) links to the test harness as it develops.
