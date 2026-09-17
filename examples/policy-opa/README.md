# Policy hook recipe

Run Conftest policies against synthetic Reeve plan inputs, then adapt them to a Terraform/OpenTofu project.
This directory tests policy input/output; it does not contain a deployable AWS workload or establish an authenticated cost-estimator integration.

## Prerequisites and local check

Install [Conftest](https://www.conftest.dev/install/) `0.70.0` and Python 3, then run:

```bash
bash examples/policy-opa/check.sh
```

The allowed fixture must pass; missing tags, a production deletion, and the wrong engine-plan shape must fail.
The change-limit script passes the small fixture and rejects the large fixture, which Reeve treats as advisory because its hook uses `on_fail: warn`.

All fixture values are synthetic and the check creates no cloud resources or persistent state.
No cleanup is needed.

## What to copy

- `policies/prevent_deletes.rego`: engine-independent rule using `env` and `counts.delete`.
- `policies/require_tags.rego`: Terraform/OpenTofu rule requiring nonempty `cost-center` and `owner` tags on every managed `aws_instance` with an after-state.
- `scripts/change-limit.py`: advisory aggregate change-count limit, passed as a command argument.
- `.reeve/terraform.yaml`: the three hook declarations; adapt its project/workspace declarations.

Run Reeve from the directory containing these `policies/` and `scripts/` paths.
For a nested root, check the process working directory rather than assuming `--root` changes it.

The tag rule deliberately rejects an absent `resource_changes` array and absent/unknown required tags.
It applies only to `aws_instance`; extend it with passing and failing fixtures for each resource type and engine shape you support.

## CI integration

Install the pinned policy tool on a prepared runner or in a custom composite-action job before Reeve runs.
The [shared workflow](../../docs/github-actions.md) does not accept arbitrary setup steps.

For the local recipe check in CI, run `bash examples/policy-opa/check.sh` after installing Conftest and Python.
[Policy hooks](../../docs/policy-hooks.md) documents the input wrapper, command environment, defaults, and result handling.
