# Examples

Each subdirectory is a self-contained `.reeve/` config plus a workflow
file. Copy whichever one matches your setup and adapt.

| Example | What it shows |
|---|---|
| [`toy-stack/`](toy-stack/) | Two Pulumi projects using the random provider - no cloud creds needed. Works for smoke-testing `reeve lint`, `reeve stacks`, `reeve plan-run`. |
| [`toy-stack-terraform/`](toy-stack-terraform/) | Two Terraform root modules (random/null providers, local backend) - no cloud creds needed. Shows the workspace=stack model; works with OpenTofu via `engine.type: tofu`. |
| [`aws-oidc/`](aws-oidc/) | Single-cloud AWS OIDC federation, S3 bucket, PR preview + apply workflow. |
| [`gcp-wif/`](gcp-wif/) | GCP Workload Identity Federation, GCS bucket. |
| [`multi-cloud/`](multi-cloud/) | AWS + GCP + secrets manager per-stack bindings, mode-scoped drift role. |
| [`drift-scheduled/`](drift-scheduled/) | Drift detection with named schedules, Slack + PagerDuty channels. |
| [`policy-opa/`](policy-opa/) | Conftest-backed policy hooks with a cost-gate script. |

## Conventions

Every example ships:

- `.reeve/*.yaml` - the reeve config
- `.github/workflows/reeve.yml` - a working Actions workflow
- `projects/<project>/Pulumi.yaml` + `Pulumi.<stack>.yaml` - minimal
  stack definitions (most examples don't ship index.ts; the point is
  the reeve wiring, not the Pulumi code)
- `README.md` - the one-time cloud setup (IAM roles, service accounts,
  App installs)

Examples are tested with `reeve lint` in CI on every push. They will
always parse - whether they apply cleanly depends on the cloud setup
you do on your side.
