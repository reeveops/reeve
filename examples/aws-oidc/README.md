# AWS OIDC wiring recipe

Single-cloud AWS using GitHub Actions OIDC federation. No long-lived AWS
credentials anywhere - reeve acquires 1-hour STS tokens per stack.

## What this example has

- `.reeve/shared.yaml` - S3 bucket, 2-of-2 approvals for prod, 4h lock TTL
- `.reeve/auth.yaml` - one `aws_oidc` provider for prod + drift-readonly
- `.reeve/pulumi.yaml` - two projects, dev/staging/prod per project
- `.github/workflows/reeve.yml` - preview + apply on comment

## Before copying

This recipe supplies configuration and a caller workflow, not workload projects.
Use an existing Pulumi project and replace all account, role, bucket, organization, and reviewer placeholders.

The caller uses a prepared runner labeled `reeve-aws` whose IAM identity can access the Reeve artifact bucket.
Workload OIDC bindings do not authenticate that controller bucket client; for hosted runners use an appropriately restricted [custom action job](../../docs/github-actions.md#composite-action-for-custom-jobs).

## One-time cloud setup

### 1. OIDC provider in AWS

```bash
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

### 2. IAM roles

Write role (`reeve-prod`):

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::111111111111:oidc-provider/token.actions.githubusercontent.com"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
      },
      "StringLike": {
        "token.actions.githubusercontent.com:sub": "repo:myorg/myrepo:*"
      }
    }
  }]
}
```

Attach whatever permissions your Pulumi stacks need to manage.

Read-only role (`reeve-drift-readonly`): same trust policy, attach
`ReadOnlyAccess` + read access to your Pulumi state bucket.

### 3. S3 bucket

```bash
aws s3api create-bucket --bucket mycompany-reeve --region us-east-1
aws s3api put-public-access-block --bucket mycompany-reeve \
  --public-access-block-configuration "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"
aws s3api put-bucket-versioning --bucket mycompany-reeve \
  --versioning-configuration Status=Enabled
```

Grant both IAM roles `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`,
`s3:ListBucket`, and `s3:GetObjectVersion` on the bucket.

### 4. Adjust the configs

Search and replace in this directory:

- `111111111111` → your AWS account ID
- `myorg/myrepo` → your GitHub org/repo
- `mycompany-reeve` → your bucket name
- `mycompany-pulumi-state` → your Pulumi state bucket
- `@org/...` → your GitHub team slugs

## Verify

```bash
cd examples/aws-oidc
reeve lint
reeve stacks              # enumerates projects/*/Pulumi.<stack>.yaml
reeve rules explain api/prod
```

## State access and validation

`engine.state.auth_provider` supplies credentials for Pulumi backend login; stack credentials override state credentials with the same environment keys during engine execution.
With an S3 state backend and AWS workload role, ensure the effective workload role can access that backend too; two roles do not remain independently active in one AWS environment.

The drift binding uses `override:` to replace the write-capable workload role.
Grant the backend permissions required by Pulumi refresh separately from read-only workload inspection.

After adapting the files, run `reeve lint`, `reeve stacks`, and `reeve rules explain YOUR_PROJECT/prod` from the consumer root.
Expect production approval rules to match `*/prod`; test with one reviewed change before enabling all projects.

This recipe does not provision cloud resources for you; remove only resources you created specifically for a disposable trial after they are no longer needed.
The evolving [cloud contract harness](https://github.com/reeveops/reeve-test/blob/master/e2e/cloud-buckets.md) tests bucket behavior separately from a workload deployment.
